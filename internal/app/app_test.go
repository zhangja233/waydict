package app

import (
	"context"
	"testing"
	"time"

	"sway-voice/internal/audio"
	"sway-voice/internal/config"
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
