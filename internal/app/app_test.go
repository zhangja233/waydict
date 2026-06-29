package app

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"sway-voice/internal/audio"
	"sway-voice/internal/config"
	"sway-voice/internal/swayipc"
	"sway-voice/internal/vad"
	"sway-voice/pkg/api"
)

func TestStateTransitionsStartStop(t *testing.T) {
	cfg := config.Defaults()
	cfg.ASR.NumThreads = 1
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	src := &audio.ScriptedSource{SampleRate: 16000, Delay: time.Millisecond}
	app := New(ctx, cfg, Dependencies{
		Source:    src,
		Segmenter: vad.NewEnergySegmenter(cfg.VAD, cfg.Audio.SampleRate),
		Engine:    &FakeEngine{Text: "hello"},
		Injector:  &MemoryInjector{},
	})
	if err := app.Start(ctx, api.ModeToggle); err != nil {
		t.Fatal(err)
	}
	if got := app.Status(ctx).State; got != api.StateListening {
		t.Fatalf("state after start = %s", got)
	}
	if err := app.Stop(ctx, false); err != nil {
		t.Fatal(err)
	}
	if got := app.Status(ctx).State; got != api.StateIdle {
		t.Fatalf("state after stop = %s", got)
	}
	if !app.Status(ctx).Injection.Available {
		t.Fatal("expected memory injector to report available")
	}
}

func TestSourceFactoryUsedOnStart(t *testing.T) {
	cfg := config.Defaults()
	cfg.ASR.NumThreads = 1
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	called := false
	app := New(ctx, cfg, Dependencies{
		SourceFactory: func() (audio.Source, error) {
			called = true
			return &audio.ScriptedSource{SampleRate: 16000, Delay: time.Millisecond}, nil
		},
		Segmenter: vad.NewEnergySegmenter(cfg.VAD, cfg.Audio.SampleRate),
		Engine:    &FakeEngine{Text: "hello"},
		Injector:  &MemoryInjector{},
	})
	if err := app.Start(ctx, api.ModeToggle); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("source factory was not called")
	}
	_ = app.Stop(ctx, false)
}

func TestAutoStopAfterSilence(t *testing.T) {
	cfg := config.Defaults()
	cfg.ASR.NumThreads = 1
	cfg.Daemon.AutoStopAfterSilenceSeconds = 1
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app := New(ctx, cfg, Dependencies{
		Source:    &audio.ScriptedSource{SampleRate: 16000, Delay: 10 * time.Millisecond},
		Segmenter: vad.NewEnergySegmenter(cfg.VAD, cfg.Audio.SampleRate),
		Engine:    &FakeEngine{Text: "hello"},
		Injector:  &MemoryInjector{},
	})
	if err := app.Start(ctx, api.ModeToggle); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if app.Status(ctx).State == api.StateIdle {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("state did not return to idle: %s", app.Status(ctx).State)
}

func TestFakeASRToInjection(t *testing.T) {
	cfg := config.Defaults()
	cfg.ASR.NumThreads = 1
	cfg.VAD.Engine = "energy"
	cfg.VAD.Threshold = 0.01
	cfg.VAD.MinSpeechMS = 20
	cfg.VAD.MinSilenceMS = 20
	cfg.VAD.SpeechPadMS = 0
	cfg.VAD.PreRollMS = 0
	cfg.VAD.MaxSpeechSeconds = 3
	chunk := func(v float32) []float32 {
		out := make([]float32, 160)
		for i := range out {
			out[i] = v
		}
		return out
	}
	src := &audio.ScriptedSource{
		SampleRate: 16000,
		Delay:      time.Millisecond,
	}
	for i := 0; i < 35; i++ {
		src.Chunks = append(src.Chunks, chunk(0.1))
	}
	for i := 0; i < 5; i++ {
		src.Chunks = append(src.Chunks, chunk(0))
	}
	mem := &MemoryInjector{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app := New(ctx, cfg, Dependencies{
		Source:    src,
		Segmenter: vad.NewEnergySegmenter(cfg.VAD, cfg.Audio.SampleRate),
		Engine:    &FakeEngine{Text: "Hello , world !"},
		Injector:  mem,
	})
	if err := app.Start(ctx, api.ModeOneshot); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(mem.Texts) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(mem.Texts) != 1 {
		t.Fatalf("expected injected text, got %v", mem.Texts)
	}
	if mem.Texts[0] != "Hello, world! " {
		t.Fatalf("postprocessed text = %q", mem.Texts[0])
	}
}

func TestFocusChangeCancelsInjection(t *testing.T) {
	socket := startFakeSway(t,
		`{"id":1,"type":"root","nodes":[{"id":2,"type":"output","name":"out","nodes":[{"id":3,"type":"workspace","name":"1","nodes":[{"id":10,"type":"con","name":"first","focused":true}]}]}]}`,
		`{"id":1,"type":"root","nodes":[{"id":2,"type":"output","name":"out","nodes":[{"id":3,"type":"workspace","name":"1","nodes":[{"id":11,"type":"con","name":"second","focused":true}]}]}]}`,
	)
	cfg := config.Defaults()
	cfg.ASR.NumThreads = 1
	cfg.VAD.Engine = "energy"
	cfg.VAD.Threshold = 0.01
	cfg.VAD.MinSpeechMS = 20
	cfg.VAD.MinSilenceMS = 20
	cfg.VAD.SpeechPadMS = 0
	cfg.VAD.PreRollMS = 0
	cfg.VAD.MaxSpeechSeconds = 3
	chunk := func(v float32) []float32 {
		out := make([]float32, 160)
		for i := range out {
			out[i] = v
		}
		return out
	}
	src := &audio.ScriptedSource{SampleRate: 16000, Delay: time.Millisecond}
	for i := 0; i < 35; i++ {
		src.Chunks = append(src.Chunks, chunk(0.1))
	}
	for i := 0; i < 5; i++ {
		src.Chunks = append(src.Chunks, chunk(0))
	}
	mem := &MemoryInjector{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app := New(ctx, cfg, Dependencies{
		Source:    src,
		Segmenter: vad.NewEnergySegmenter(cfg.VAD, cfg.Audio.SampleRate),
		Engine:    &FakeEngine{Text: "secret"},
		Injector:  mem,
		Focus:     swayipc.New(socket),
	})
	if err := app.Start(ctx, api.ModeToggle); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		st := app.Status(ctx)
		if st.LastError != nil && st.LastError.Code == "focus_changed" {
			if len(mem.Texts) != 0 {
				t.Fatalf("text was injected despite focus change: %v", mem.Texts)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("focus change was not recorded; status=%+v injected=%v", app.Status(ctx), mem.Texts)
}

func startFakeSway(t *testing.T, trees ...string) string {
	t.Helper()
	socket := filepath.Join(t.TempDir(), "sway.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = ln.Close()
		_ = os.Remove(socket)
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; ; i++ {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			tree := trees[len(trees)-1]
			if i < len(trees) {
				tree = trees[i]
			}
			serveSwayTree(t, conn, []byte(tree))
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("fake sway server did not stop")
		}
	})
	return socket
}

func serveSwayTree(t *testing.T, conn net.Conn, payload []byte) {
	t.Helper()
	defer conn.Close()
	header := make([]byte, 14)
	if _, err := io.ReadFull(conn, header); err != nil {
		t.Errorf("read header: %v", err)
		return
	}
	if string(header[:6]) != "i3-ipc" {
		t.Errorf("bad magic %q", header[:6])
		return
	}
	length := binary.LittleEndian.Uint32(header[6:10])
	if length > 0 {
		body := make([]byte, length)
		if _, err := io.ReadFull(conn, body); err != nil {
			t.Errorf("read body: %v", err)
			return
		}
	}
	typ := binary.LittleEndian.Uint32(header[10:14])
	resp := make([]byte, 14+len(payload))
	copy(resp[:6], "i3-ipc")
	binary.LittleEndian.PutUint32(resp[6:10], uint32(len(payload)))
	binary.LittleEndian.PutUint32(resp[10:14], typ)
	copy(resp[14:], payload)
	if _, err := conn.Write(resp); err != nil {
		t.Errorf("write response: %v", err)
	}
}
