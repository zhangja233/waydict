package app

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"sway-voice/internal/asr"
	"sway-voice/internal/audio"
	"sway-voice/internal/config"
	"sway-voice/internal/control"
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

func TestStatusReportsSegmentOpen(t *testing.T) {
	cfg := config.Defaults()
	cfg.ASR.NumThreads = 1
	cfg.VAD.Engine = "energy"
	cfg.VAD.Threshold = 0.01
	cfg.VAD.MinSpeechMS = 20
	cfg.VAD.MinSilenceMS = 1000
	cfg.VAD.SpeechPadMS = 0
	cfg.VAD.PreRollMS = 0
	cfg.VAD.MaxSpeechSeconds = 3
	speech := make([]float32, 320)
	for i := range speech {
		speech[i] = 0.1
	}
	src := &audio.ScriptedSource{
		SampleRate: 16000,
		Chunks:     [][]float32{speech, speech, speech},
		Delay:      time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app := New(ctx, cfg, Dependencies{
		Source:    src,
		Segmenter: vad.NewEnergySegmenter(cfg.VAD, cfg.Audio.SampleRate),
		Engine:    &FakeEngine{Text: "hello", IsLoaded: true},
		Injector:  &MemoryInjector{},
	})
	if err := app.Start(ctx, api.ModeToggle); err != nil {
		t.Fatal(err)
	}
	defer app.Stop(ctx, false)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if got := app.Status(ctx).State; got == api.StateSegmentOpen {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("state did not become segment_open: %s", app.Status(ctx).State)
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

func TestSourceFactoryRetriesAfterStartFailure(t *testing.T) {
	cfg := config.Defaults()
	cfg.ASR.NumThreads = 1
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	failed := &startFailSource{sampleRate: 16000}
	calls := 0
	app := New(ctx, cfg, Dependencies{
		SourceFactory: func() (audio.Source, error) {
			calls++
			if calls == 1 {
				return failed, nil
			}
			return &audio.ScriptedSource{SampleRate: 16000, Delay: time.Millisecond}, nil
		},
		Segmenter: vad.NewEnergySegmenter(cfg.VAD, cfg.Audio.SampleRate),
		Engine:    &FakeEngine{Text: "hello", IsLoaded: true},
		Injector:  &MemoryInjector{},
	})
	if err := app.Start(ctx, api.ModeToggle); err == nil {
		t.Fatal("expected first start to fail")
	}
	if !failed.closed {
		t.Fatal("failed source was not closed")
	}
	if err := app.Start(ctx, api.ModeToggle); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("factory calls = %d, want 2", calls)
	}
	_ = app.Stop(ctx, false)
}

func TestStartReturnsCodedDependencyErrors(t *testing.T) {
	cfg := config.Defaults()
	cfg.ASR.NumThreads = 1
	tests := []struct {
		name string
		deps Dependencies
		want string
	}{
		{
			name: "wtype",
			deps: Dependencies{
				Engine:   &FakeEngine{Text: "hello", IsLoaded: true},
				Injector: &MemoryInjector{Err: errors.New("missing executable")},
			},
			want: "wtype_unavailable",
		},
		{
			name: "model",
			deps: Dependencies{
				ModelChecker: func() error { return errors.New("missing files") },
				Engine:       &FakeEngine{Text: "hello"},
				Injector:     &MemoryInjector{},
			},
			want: "model_invalid",
		},
		{
			name: "pipewire",
			deps: Dependencies{
				SourceFactory: func() (audio.Source, error) { return nil, errors.New("connect failed") },
				Engine:        &FakeEngine{Text: "hello", IsLoaded: true},
				Injector:      &MemoryInjector{},
			},
			want: "pipewire_unavailable",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			app := New(ctx, cfg, tc.deps)
			resp := app.HandleControl(ctx, control.NewRequest("start", map[string]any{}))
			if resp.OK || resp.Error == nil || resp.Error.Code != tc.want {
				t.Fatalf("response error = %+v, want %s", resp.Error, tc.want)
			}
		})
	}
}

func TestStopRejectsCommitDiscardConflict(t *testing.T) {
	cfg := config.Defaults()
	cfg.ASR.NumThreads = 1
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app := New(ctx, cfg, Dependencies{
		Engine:   &FakeEngine{Text: "hello", IsLoaded: true},
		Injector: &MemoryInjector{},
	})
	resp := app.HandleControl(ctx, control.NewRequest("stop", map[string]any{
		"commit":  true,
		"discard": true,
	}))
	if resp.OK || resp.Error == nil || resp.Error.Code != "usage" {
		t.Fatalf("response error = %+v, want usage", resp.Error)
	}
}

func TestReloadConfigAppliesReloadableFields(t *testing.T) {
	cfg := config.Defaults()
	cfg.ASR.NumThreads = 1
	next := cfg
	next.Daemon.RedactTranscriptsInLogs = false
	next.Daemon.AutoStopAfterSilenceSeconds = cfg.Daemon.AutoStopAfterSilenceSeconds + 1
	next.Injection.AppendSpace = false
	next.Debug.SaveAudioSegments = true
	next.Debug.SaveAudioDir = t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app := New(ctx, cfg, Dependencies{
		ConfigReloader: func(context.Context) (config.Config, error) { return next, nil },
	})
	resp := app.HandleControl(ctx, control.NewRequest("reload_config", nil))
	if !resp.OK {
		t.Fatalf("reload failed: %+v", resp.Error)
	}
	if app.cfg.Daemon.RedactTranscriptsInLogs {
		t.Fatal("redaction setting was not reloaded")
	}
	if app.cfg.Daemon.AutoStopAfterSilenceSeconds != next.Daemon.AutoStopAfterSilenceSeconds {
		t.Fatal("auto-stop setting was not reloaded")
	}
	if got := app.post.Apply("hello"); got != "hello" {
		t.Fatalf("postprocessor text = %q, want no appended space", got)
	}
	if !app.cfg.Debug.SaveAudioSegments || app.cfg.Debug.SaveAudioDir != next.Debug.SaveAudioDir {
		t.Fatal("debug audio settings were not reloaded")
	}
}

func TestReloadConfigRejectsRuntimeChanges(t *testing.T) {
	cfg := config.Defaults()
	cfg.ASR.NumThreads = 1
	next := cfg
	next.ASR.ModelDir = filepath.Join(t.TempDir(), "other-model")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app := New(ctx, cfg, Dependencies{
		ConfigReloader: func(context.Context) (config.Config, error) { return next, nil },
	})
	resp := app.HandleControl(ctx, control.NewRequest("reload_config", nil))
	if resp.OK || resp.Error == nil || resp.Error.Code != "restart_required" {
		t.Fatalf("response error = %+v, want restart_required", resp.Error)
	}
}

func TestReloadConfigRejectsActiveCapture(t *testing.T) {
	cfg := config.Defaults()
	cfg.ASR.NumThreads = 1
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app := New(ctx, cfg, Dependencies{
		Source:         &audio.ScriptedSource{SampleRate: 16000, Delay: time.Millisecond},
		Engine:         &FakeEngine{Text: "hello", IsLoaded: true},
		Injector:       &MemoryInjector{},
		ConfigReloader: func(context.Context) (config.Config, error) { return cfg, nil },
	})
	if err := app.Start(ctx, api.ModeToggle); err != nil {
		t.Fatal(err)
	}
	defer app.Stop(ctx, false)
	resp := app.HandleControl(ctx, control.NewRequest("reload_config", nil))
	if resp.OK || resp.Error == nil || resp.Error.Code != "busy" {
		t.Fatalf("response error = %+v, want busy", resp.Error)
	}
}

func TestStopDiscardSuppressesPendingInjection(t *testing.T) {
	cfg := config.Defaults()
	cfg.ASR.NumThreads = 1
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	engine := &gateEngine{
		text:    "secret",
		started: make(chan struct{}, 1),
		release: make(chan struct{}),
		done:    make(chan struct{}),
	}
	mem := &MemoryInjector{}
	app := New(ctx, cfg, Dependencies{
		Engine:   engine,
		Injector: mem,
	})
	app.queueSegment(asr.AudioSegment{ID: "pending", Duration: time.Second})
	select {
	case <-engine.started:
	case <-time.After(time.Second):
		t.Fatal("ASR worker did not start")
	}
	if err := app.Stop(ctx, false); err != nil {
		t.Fatal(err)
	}
	close(engine.release)
	select {
	case <-engine.done:
	case <-time.After(time.Second):
		t.Fatal("ASR worker did not finish")
	}
	if len(mem.Texts) != 0 {
		t.Fatalf("discarded text was injected: %v", mem.Texts)
	}
	if got := app.Status(ctx).State; got != api.StateIdle {
		t.Fatalf("state = %s, want idle", got)
	}
}

func TestStopCommitAllowsPendingInjection(t *testing.T) {
	cfg := config.Defaults()
	cfg.ASR.NumThreads = 1
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	engine := &gateEngine{
		text:    "committed",
		started: make(chan struct{}, 1),
		release: make(chan struct{}),
		done:    make(chan struct{}),
	}
	mem := &MemoryInjector{}
	app := New(ctx, cfg, Dependencies{
		Engine:   engine,
		Injector: mem,
	})
	app.queueSegment(asr.AudioSegment{ID: "pending", Duration: time.Second})
	select {
	case <-engine.started:
	case <-time.After(time.Second):
		t.Fatal("ASR worker did not start")
	}
	if err := app.Stop(ctx, true); err != nil {
		t.Fatal(err)
	}
	close(engine.release)
	select {
	case <-engine.done:
	case <-time.After(time.Second):
		t.Fatal("ASR worker did not finish")
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(mem.Texts) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(mem.Texts) != 1 || mem.Texts[0] != "committed " {
		t.Fatalf("committed text was not injected: %v", mem.Texts)
	}
	if got := app.Status(ctx).State; got != api.StateIdle {
		t.Fatalf("state = %s, want idle", got)
	}
}

func TestStopCommitFlushesOpenSpeechSegment(t *testing.T) {
	cfg := config.Defaults()
	cfg.ASR.NumThreads = 1
	cfg.VAD.Engine = "energy"
	cfg.VAD.Threshold = 0.01
	cfg.VAD.MinSpeechMS = 20
	cfg.VAD.MinSilenceMS = 1000
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
	for i := 0; i < 8; i++ {
		src.Chunks = append(src.Chunks, chunk(0.1))
	}
	mem := &MemoryInjector{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app := New(ctx, cfg, Dependencies{
		Source:    src,
		Segmenter: vad.NewEnergySegmenter(cfg.VAD, cfg.Audio.SampleRate),
		Engine:    &FakeEngine{Text: "flushed", IsLoaded: true},
		Injector:  mem,
	})
	if err := app.Start(ctx, api.ModeHold); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if app.Status(ctx).State == api.StateSegmentOpen {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := app.Status(ctx).State; got != api.StateSegmentOpen {
		t.Fatalf("state = %s, want segment_open before commit", got)
	}
	if err := app.Stop(ctx, true); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(mem.Texts) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(mem.Texts) != 1 || mem.Texts[0] != "flushed " {
		t.Fatalf("flushed text was not injected: %v", mem.Texts)
	}
	if got := app.Status(ctx).State; got != api.StateIdle {
		t.Fatalf("state = %s, want idle", got)
	}
}

func TestStopDiscardSuppressesPendingASRError(t *testing.T) {
	cfg := config.Defaults()
	cfg.ASR.NumThreads = 1
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	engine := &gateEngine{
		err:     fmt.Errorf("decode failed"),
		started: make(chan struct{}, 1),
		release: make(chan struct{}),
		done:    make(chan struct{}),
	}
	app := New(ctx, cfg, Dependencies{
		Engine:   engine,
		Injector: &MemoryInjector{},
	})
	app.queueSegment(asr.AudioSegment{ID: "pending", Duration: time.Second})
	select {
	case <-engine.started:
	case <-time.After(time.Second):
		t.Fatal("ASR worker did not start")
	}
	if err := app.Stop(ctx, false); err != nil {
		t.Fatal(err)
	}
	close(engine.release)
	select {
	case <-engine.done:
	case <-time.After(time.Second):
		t.Fatal("ASR worker did not finish")
	}
	st := app.Status(ctx)
	if st.LastError != nil {
		t.Fatalf("discarded ASR error leaked into status: %+v", st.LastError)
	}
	if st.State != api.StateIdle {
		t.Fatalf("state = %s, want idle", st.State)
	}
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

func TestWtypeFailureRetentionFollowsRedaction(t *testing.T) {
	tests := []struct {
		name       string
		redact     bool
		wantStored string
	}{
		{name: "redacted", redact: true, wantStored: ""},
		{name: "unredacted", redact: false, wantStored: "secret "},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Defaults()
			cfg.ASR.NumThreads = 1
			cfg.Daemon.RedactTranscriptsInLogs = tc.redact
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			app := New(ctx, cfg, Dependencies{
				Engine:   &FakeEngine{Text: "secret", IsLoaded: true},
				Injector: &MemoryInjector{Err: errors.New("wtype failed")},
			})
			app.queueSegment(asr.AudioSegment{ID: "seg", Duration: time.Second})
			deadline := time.Now().Add(time.Second)
			for time.Now().Before(deadline) {
				st := app.Status(ctx)
				if st.LastError != nil {
					if st.LastError.Code != "wtype_failed" {
						t.Fatalf("error code = %s, want wtype_failed", st.LastError.Code)
					}
					if st.LastUninjectedText != tc.wantStored {
						t.Fatalf("last uninjected text = %q, want %q", st.LastUninjectedText, tc.wantStored)
					}
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
			t.Fatalf("wtype failure was not recorded: %+v", app.Status(ctx))
		})
	}
}

func TestCaptureOverrunMarksSegment(t *testing.T) {
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
	src := &overrunSource{sampleRate: 16000}
	for i := 0; i < 35; i++ {
		src.chunks = append(src.chunks, chunk(0.1))
	}
	for i := 0; i < 5; i++ {
		src.chunks = append(src.chunks, chunk(0))
	}
	engine := &recordingEngine{seen: make(chan asr.AudioSegment, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app := New(ctx, cfg, Dependencies{
		Source:    src,
		Segmenter: vad.NewEnergySegmenter(cfg.VAD, cfg.Audio.SampleRate),
		Engine:    engine,
		Injector:  &MemoryInjector{},
	})
	if err := app.Start(ctx, api.ModeToggle); err != nil {
		t.Fatal(err)
	}
	select {
	case seg := <-engine.seen:
		if !seg.CaptureOverrun || !seg.Degraded {
			t.Fatalf("segment overrun flags were not set: %+v", seg)
		}
	case <-time.After(time.Second):
		t.Fatal("ASR did not receive a segment")
	}
}

func TestCaptureErrorResetsSegmenter(t *testing.T) {
	cfg := config.Defaults()
	cfg.ASR.NumThreads = 1
	seg := &resetTrackingSegmenter{reset: make(chan struct{}, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app := New(ctx, cfg, Dependencies{
		Source:    &errorSource{sampleRate: 16000},
		Segmenter: seg,
		Engine:    &FakeEngine{Text: "hello"},
		Injector:  &MemoryInjector{},
	})
	if err := app.Start(ctx, api.ModeToggle); err != nil {
		t.Fatal(err)
	}
	select {
	case <-seg.reset:
	case <-time.After(time.Second):
		t.Fatal("segmenter was not reset after capture error")
	}
	st := app.Status(ctx)
	if st.LastError == nil || st.LastError.Code != "pipewire_unavailable" {
		t.Fatalf("unexpected status after capture error: %+v", st.LastError)
	}
}

func TestQueueSegmentDoesNotBlockWhenASRBackedUp(t *testing.T) {
	cfg := config.Defaults()
	cfg.ASR.NumThreads = 1
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	engine := &blockingEngine{started: make(chan struct{}, 1)}
	app := New(ctx, cfg, Dependencies{
		Engine:   engine,
		Injector: &MemoryInjector{},
	})
	app.queueSegment(asr.AudioSegment{ID: "active", Duration: time.Second})
	select {
	case <-engine.started:
	case <-time.After(time.Second):
		t.Fatal("ASR worker did not start")
	}
	for i := 0; i < cap(app.asrQueue); i++ {
		app.queueSegment(asr.AudioSegment{ID: "queued", Duration: time.Second})
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		app.queueSegment(asr.AudioSegment{ID: "overflow", Duration: time.Second})
	}()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("queueSegment blocked on a full ASR queue")
	}
	st := app.Status(ctx)
	if st.LastError == nil || st.LastError.Code != "recognition_failed" {
		t.Fatalf("unexpected status error: %+v", st.LastError)
	}
}

func TestASRDeadlineRecordsTimeout(t *testing.T) {
	cfg := config.Defaults()
	cfg.ASR.NumThreads = 1
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app := New(ctx, cfg, Dependencies{
		Engine:   &errorEngine{err: context.DeadlineExceeded},
		Injector: &MemoryInjector{},
	})
	app.queueSegment(asr.AudioSegment{ID: "timeout", Duration: time.Second})
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		st := app.Status(ctx)
		if st.LastError != nil {
			if st.LastError.Code != "recognition_timeout" {
				t.Fatalf("error code = %s, want recognition_timeout", st.LastError.Code)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout error was not recorded: %+v", app.Status(ctx).LastError)
}

func TestDebugSaveAudioSegments(t *testing.T) {
	cfg := config.Defaults()
	cfg.ASR.NumThreads = 1
	cfg.Debug.SaveAudioSegments = true
	cfg.Debug.SaveAudioDir = t.TempDir()
	engine := &recordingEngine{seen: make(chan asr.AudioSegment, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app := New(ctx, cfg, Dependencies{
		Engine:   engine,
		Injector: &MemoryInjector{},
	})
	app.queueSegment(asr.AudioSegment{
		ID:         "seg/one",
		Samples:    []float32{0.25, -0.25},
		SampleRate: 16000,
		Duration:   time.Millisecond,
	})
	select {
	case <-engine.seen:
	case <-time.After(time.Second):
		t.Fatal("ASR did not receive segment")
	}
	path := filepath.Join(cfg.Debug.SaveAudioDir, "seg_one.wav")
	got, err := audio.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Samples) != 2 || got.Samples[0] != 0.25 || got.Samples[1] != -0.25 {
		t.Fatalf("saved samples = %v", got.Samples)
	}
}

func TestDebugSaveAudioSegmentsDefaultOff(t *testing.T) {
	cfg := config.Defaults()
	cfg.ASR.NumThreads = 1
	cfg.Debug.SaveAudioDir = filepath.Join(t.TempDir(), "segments")
	engine := &recordingEngine{seen: make(chan asr.AudioSegment, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app := New(ctx, cfg, Dependencies{
		Engine:   engine,
		Injector: &MemoryInjector{},
	})
	app.queueSegment(asr.AudioSegment{
		ID:         "seg",
		Samples:    []float32{0.25},
		SampleRate: 16000,
		Duration:   time.Millisecond,
	})
	select {
	case <-engine.seen:
	case <-time.After(time.Second):
		t.Fatal("ASR did not receive segment")
	}
	if _, err := os.Stat(cfg.Debug.SaveAudioDir); !os.IsNotExist(err) {
		t.Fatalf("debug save dir exists with default disabled: %v", err)
	}
}

type resetTrackingSegmenter struct {
	reset chan struct{}
}

func (s *resetTrackingSegmenter) Feed([]float32, time.Time) []asr.AudioSegment {
	return nil
}

func (s *resetTrackingSegmenter) Flush(bool, time.Time) []asr.AudioSegment {
	return nil
}

func (s *resetTrackingSegmenter) Reset() {
	select {
	case s.reset <- struct{}{}:
	default:
	}
}

type errorSource struct {
	sampleRate int
}

func (s *errorSource) Start(context.Context) error { return nil }
func (s *errorSource) Pause(context.Context) error { return nil }
func (s *errorSource) Stop(context.Context) error  { return nil }

func (s *errorSource) Read(context.Context, []float32) (int, error) {
	return 0, audio.ErrUnavailable
}

func (s *errorSource) Stats() audio.Stats {
	return audio.Stats{SampleRate: s.sampleRate}
}

type startFailSource struct {
	sampleRate int
	closed     bool
}

func (s *startFailSource) Start(context.Context) error { return audio.ErrUnavailable }
func (s *startFailSource) Pause(context.Context) error { return nil }
func (s *startFailSource) Stop(context.Context) error  { return nil }

func (s *startFailSource) Read(context.Context, []float32) (int, error) {
	return 0, audio.ErrUnavailable
}

func (s *startFailSource) Stats() audio.Stats {
	return audio.Stats{SampleRate: s.sampleRate}
}

func (s *startFailSource) Close() {
	s.closed = true
}

type recordingEngine struct {
	FakeEngine
	seen chan asr.AudioSegment
}

func (r *recordingEngine) Transcribe(ctx context.Context, seg asr.AudioSegment) (asr.Transcript, error) {
	r.seen <- seg
	return asr.Transcript{SegmentID: seg.ID, Empty: true}, nil
}

type blockingEngine struct {
	FakeEngine
	started chan struct{}
}

func (b *blockingEngine) Transcribe(ctx context.Context, seg asr.AudioSegment) (asr.Transcript, error) {
	select {
	case b.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return asr.Transcript{}, ctx.Err()
}

type gateEngine struct {
	FakeEngine
	text    string
	err     error
	started chan struct{}
	release chan struct{}
	done    chan struct{}
}

func (g *gateEngine) Transcribe(ctx context.Context, seg asr.AudioSegment) (asr.Transcript, error) {
	select {
	case g.started <- struct{}{}:
	default:
	}
	select {
	case <-g.release:
	case <-ctx.Done():
		close(g.done)
		return asr.Transcript{}, ctx.Err()
	}
	close(g.done)
	if g.err != nil {
		return asr.Transcript{}, g.err
	}
	return asr.Transcript{
		SegmentID:     seg.ID,
		Text:          g.text,
		StartedAt:     seg.StartedAt,
		AudioDuration: seg.Duration,
		Empty:         g.text == "",
	}, nil
}

type errorEngine struct {
	FakeEngine
	err error
}

func (e *errorEngine) Transcribe(context.Context, asr.AudioSegment) (asr.Transcript, error) {
	return asr.Transcript{}, e.err
}

type overrunSource struct {
	chunks     [][]float32
	sampleRate int
	capturing  bool
	index      int
}

func (s *overrunSource) Start(context.Context) error {
	s.capturing = true
	return nil
}

func (s *overrunSource) Pause(context.Context) error {
	s.capturing = false
	return nil
}

func (s *overrunSource) Stop(context.Context) error {
	s.capturing = false
	return nil
}

func (s *overrunSource) Read(ctx context.Context, dst []float32) (int, error) {
	if !s.capturing || s.index >= len(s.chunks) {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(time.Millisecond):
			return 0, nil
		}
	}
	chunk := s.chunks[s.index]
	s.index++
	return copy(dst, chunk), nil
}

func (s *overrunSource) Stats() audio.Stats {
	overruns := uint64(0)
	if s.index > 0 {
		overruns = 1
	}
	return audio.Stats{SampleRate: s.sampleRate, Capturing: s.capturing, Overruns: overruns}
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
