package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"waydict/internal/apperr"
	"waydict/internal/asr"
	"waydict/internal/audio"
	"waydict/internal/config"
	"waydict/internal/focus"
	"waydict/internal/inject"
	"waydict/pkg/api"
)

func TestRuntimeWiresFactoriesServesAndCloses(t *testing.T) {
	cfg := runtimeTestConfig(t)
	var sourceCalls atomic.Int32
	var sourceClosed atomic.Bool
	opts := RuntimeOptions{Platform: PlatformDependencies{
		Name: "test",
		NewSource: func(config.Audio) (audio.Source, error) {
			sourceCalls.Add(1)
			return &runtimeSource{closed: &sourceClosed}, nil
		},
		NewInjector: func(config.Injection) inject.Injector { return &MemoryInjector{} },
	}}
	ctx, cancel := context.WithCancel(context.Background())
	runtime, err := NewRuntime(ctx, cfg, opts)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if runtime.App == nil || runtime.Server == nil || runtime.Platform.Name != "test" {
		t.Fatalf("runtime = %#v", runtime)
	}
	if err := runtime.RecreateAudio(ctx); err != nil {
		t.Fatal(err)
	}
	if sourceCalls.Load() != 1 {
		t.Fatalf("source factory calls = %d", sourceCalls.Load())
	}
	served := make(chan error, 1)
	go func() { served <- runtime.Serve(ctx) }()
	deadline := time.Now().Add(time.Second)
	for {
		if _, err := os.Stat(cfg.Daemon.Socket); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("control socket was not created")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	if err := <-served; err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if err := runtime.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !sourceClosed.Load() {
		t.Fatal("audio source was not closed")
	}
	if err := runtime.Close(context.Background()); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestRuntimeFocusStartupCanFailFastOrDegrade(t *testing.T) {
	for _, allow := range []bool{false, true} {
		t.Run(map[bool]string{false: "fail_fast", true: "degraded"}[allow], func(t *testing.T) {
			cfg := runtimeTestConfig(t)
			cfg.Focus.Enabled = true
			cfg.Focus.Required = true
			opts := RuntimeOptions{
				AllowDegradedStartup: allow,
				Platform: PlatformDependencies{
					Name:             "test",
					NewFocusProvider: func(config.Focus) focus.Provider { return unavailableRuntimeFocus{} },
				},
			}
			runtime, err := NewRuntime(context.Background(), cfg, opts)
			if !allow {
				if apperr.Code(err) != apperr.CodeFocusUnavailable {
					t.Fatalf("error = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer runtime.Close(context.Background())
			status := runtime.App.Status(context.Background())
			if status.LastError == nil || status.LastError.Code != apperr.CodeFocusUnavailable {
				t.Fatalf("status error = %#v", status.LastError)
			}
		})
	}
}

func TestRuntimeSuspendDiscardsSessionAndReleasesAudio(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := &recoverySource{}
	application := New(ctx, config.Defaults(), Dependencies{
		Source:   source,
		Engine:   &FakeEngine{IsLoaded: true},
		Injector: &MemoryInjector{},
	})
	runtime := &Runtime{App: application}
	if err := application.Start(ctx, api.ModeToggle); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Suspend(context.Background()); err != nil {
		t.Fatal(err)
	}
	status := application.Status(context.Background())
	if status.State != api.StateIdle || status.Audio.Capturing || application.source != nil {
		t.Fatalf("status after suspend = %+v source=%T", status, application.source)
	}
	if !source.stopped.Load() || !source.closed.Load() {
		t.Fatalf("source stopped=%t closed=%t", source.stopped.Load(), source.closed.Load())
	}
}

func TestCriticalMemoryPressureUnloadsOnlyWhileIdleAndNextStartReloads(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := &FakeEngine{IsLoaded: true}
	second := &FakeEngine{}
	var application *App
	application = New(ctx, config.Defaults(), Dependencies{
		Source: &audio.ScriptedSource{SampleRate: 16000, Delay: time.Millisecond},
		Engine: first,
		EnsureASR: func(context.Context) error {
			application.setASREngine(second, asr.Resolution{Engine: asr.EngineSherpa, Provider: asr.ProviderCPU})
			return nil
		},
	})
	runtime := &Runtime{App: application}
	unloaded, err := runtime.ReleaseASRForMemoryPressure()
	if err != nil || !unloaded || first.Loaded() || application.Status(ctx).ASR.Loaded {
		t.Fatalf("release result unloaded=%t err=%v first_loaded=%t status=%+v", unloaded, err, first.Loaded(), application.Status(ctx).ASR)
	}
	if err := application.Start(ctx, api.ModeOneshot); err != nil {
		t.Fatal(err)
	}
	if !second.Loaded() {
		t.Fatal("next start did not reload ASR")
	}
	_ = application.Stop(ctx, false)
}

func runtimeTestConfig(t *testing.T) config.Config {
	t.Helper()
	cfg := config.Defaults()
	cfg.Daemon.Socket = filepath.Join(t.TempDir(), "control.sock")
	cfg.Daemon.PreloadModel = false
	cfg.VAD.Engine = "energy"
	cfg.ASR.Engine = asr.EngineSherpa
	cfg.ASR.Provider = asr.ProviderCPU
	cfg.Focus.Enabled = false
	cfg.Focus.Required = false
	return cfg
}

type runtimeSource struct {
	closed *atomic.Bool
}

type recoverySource struct {
	stopped atomic.Bool
	closed  atomic.Bool
}

func (*recoverySource) Start(context.Context) error { return nil }
func (*recoverySource) Pause(context.Context) error { return nil }
func (s *recoverySource) Stop(context.Context) error {
	s.stopped.Store(true)
	return nil
}
func (*recoverySource) Read(ctx context.Context, _ []float32) (int, error) {
	<-ctx.Done()
	return 0, ctx.Err()
}
func (*recoverySource) Stats() audio.Stats { return audio.Stats{Backend: "test", SampleRate: 16000} }
func (s *recoverySource) Close()           { s.closed.Store(true) }

func (*runtimeSource) Start(context.Context) error                  { return nil }
func (*runtimeSource) Pause(context.Context) error                  { return nil }
func (*runtimeSource) Stop(context.Context) error                   { return nil }
func (*runtimeSource) Read(context.Context, []float32) (int, error) { return 0, nil }
func (*runtimeSource) Stats() audio.Stats                           { return audio.Stats{Backend: "test", SampleRate: 16000} }
func (s *runtimeSource) Close()                                     { s.closed.Store(true) }

type unavailableRuntimeFocus struct{}

func (unavailableRuntimeFocus) Backend() string { return "test" }
func (unavailableRuntimeFocus) Available(context.Context) error {
	return apperr.New(apperr.CodeFocusUnavailable, "check focus", errors.New("unavailable"))
}
func (unavailableRuntimeFocus) Current(context.Context) (focus.Target, error) {
	return focus.Target{}, apperr.New(apperr.CodeFocusUnavailable, "read focus", errors.New("unavailable"))
}
func (unavailableRuntimeFocus) Same(context.Context, focus.Target) (focus.Target, bool, error) {
	return focus.Target{}, false, apperr.New(apperr.CodeFocusUnavailable, "compare focus", errors.New("unavailable"))
}
func (unavailableRuntimeFocus) Release(focus.Target) {}
