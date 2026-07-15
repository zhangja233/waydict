package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"waydict/internal/apperr"
	"waydict/internal/asr"
	"waydict/internal/audio"
	"waydict/internal/config"
	"waydict/internal/control"
	"waydict/internal/focus"
	"waydict/internal/inject"
	"waydict/internal/permissions"
	"waydict/internal/preferences"
	"waydict/internal/vad"
	"waydict/pkg/api"
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

func TestStatusReportsVADEngine(t *testing.T) {
	cfg := config.Defaults()
	cfg.ASR.NumThreads = 1
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app := New(ctx, cfg, Dependencies{
		Source:    &audio.ScriptedSource{SampleRate: 16000, Delay: time.Millisecond},
		Segmenter: vad.NewEnergySegmenter(cfg.VAD, cfg.Audio.SampleRate),
		Engine:    &FakeEngine{},
		Injector:  &MemoryInjector{},
	})
	if got := app.Status(ctx).VAD.Engine; got != "energy" {
		t.Fatalf("vad engine = %q, want energy", got)
	}
}

func TestStatusJSONIncludesResolvedASRFields(t *testing.T) {
	cfg := config.Defaults()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app := New(ctx, cfg, Dependencies{
		Engine: &FakeEngine{},
		ASRResolution: asr.Resolution{
			Engine:         asr.EngineSherpa,
			Provider:       asr.ProviderCPU,
			FallbackReason: "gpu unavailable",
		},
	})
	data, err := json.Marshal(app.Status(ctx))
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		ASR map[string]any `json:"asr"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"resolved_engine", "resolved_provider", "gpu_name", "fallback_reason"} {
		if _, ok := payload.ASR[field]; !ok {
			t.Fatalf("ASR status JSON missing %q: %s", field, data)
		}
	}
}

func TestWhisperBackendDowngradeUpdatesStatus(t *testing.T) {
	cfg := config.Defaults()
	cfg.ASR.Engine = asr.EngineWhisper
	cfg.ASR.Provider = asr.ProviderVulkan
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	engine := &backendEngine{backend: "cpu", gpu: false}
	app := New(ctx, cfg, Dependencies{
		Engine: engine,
		ASRResolution: asr.Resolution{
			Engine:   asr.EngineWhisper,
			Provider: asr.ProviderVulkan,
			GPUName:  "probed gpu",
		},
	})
	if err := app.loadASR(ctx, 0); err != nil {
		t.Fatal(err)
	}
	status := app.Status(ctx).ASR
	if status.ResolvedProvider != asr.ProviderCPU || status.GPUName != "" {
		t.Fatalf("ASR status = %+v, want reported CPU backend", status)
	}
}

func TestAutoWhisperLoadFailureFallsBackToSherpa(t *testing.T) {
	cfg := config.Defaults()
	cfg.ASR.NumThreads = 1
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	whisper := &loadErrorEngine{err: errors.New("gpu load failed")}
	sherpa := &FakeEngine{}
	fallbackCalls := 0
	app := New(ctx, cfg, Dependencies{
		Engine: whisper,
		ASRResolution: asr.Resolution{
			Engine:   asr.EngineWhisper,
			Provider: asr.ProviderVulkan,
		},
		ASRFallback: func() (asr.Engine, asr.Resolution, error) {
			fallbackCalls++
			return sherpa, asr.Resolution{Engine: asr.EngineSherpa, Provider: asr.ProviderCPU}, nil
		},
	})
	if err := app.loadASR(ctx, 0); err != nil {
		t.Fatal(err)
	}
	status := app.Status(ctx).ASR
	if fallbackCalls != 1 || app.engine != sherpa || status.ResolvedEngine != asr.EngineSherpa || status.FallbackReason == "" {
		t.Fatalf("fallback calls=%d engine=%T status=%+v", fallbackCalls, app.engine, status)
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

func TestSourceFactoryRetriesStartFailure(t *testing.T) {
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
	if err := app.Start(ctx, api.ModeToggle); err != nil {
		t.Fatal(err)
	}
	if !failed.closed {
		t.Fatal("failed source was not closed")
	}
	if calls != 2 {
		t.Fatalf("factory calls = %d, want 2", calls)
	}
	_ = app.Stop(ctx, false)
}

func TestAudioDeviceAddedEventRecreatesIdleSource(t *testing.T) {
	cfg := config.Defaults()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan audio.Event, 1)
	recreated := make(chan struct{}, 1)
	New(ctx, cfg, Dependencies{
		Source: &eventAudioSource{events: events},
		AudioSourceFactory: func(config.Audio) (audio.Source, error) {
			recreated <- struct{}{}
			return &audio.ScriptedSource{SampleRate: 16000, Delay: time.Millisecond}, nil
		},
	})
	events <- audio.Event{Kind: audio.EventDeviceAdded, DeviceID: "device-1", At: time.Now()}
	select {
	case <-recreated:
	case <-time.After(time.Second):
		t.Fatal("device-added event did not recreate the idle source")
	}
}

func TestAudioRetryabilityIsTyped(t *testing.T) {
	nonRetryable := apperr.New(apperr.CodeAudioDeviceNotFound, "select input", errors.New("missing"))
	if audioRetryable(nonRetryable) {
		t.Fatal("missing-device error must not retry")
	}
	retryable := &apperr.Error{Code: apperr.CodeAudioDeviceDisconnected, Operation: "read input", Retryable: true, Err: errors.New("disconnected")}
	if !audioRetryable(retryable) {
		t.Fatal("typed transient error must retry")
	}
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
			name: "injector",
			deps: Dependencies{
				Engine:   &FakeEngine{Text: "hello", IsLoaded: true},
				Injector: &MemoryInjector{err: errors.New("missing executable")},
			},
			want: apperr.CodeInjectorUnavailable,
		},
		{
			name: "model",
			deps: Dependencies{
				ModelChecker: func(string) error { return errors.New("missing files") },
				Engine:       &FakeEngine{Text: "hello"},
				ASRResolution: asr.Resolution{
					Engine:   asr.EngineSherpa,
					Provider: asr.ProviderCPU,
				},
				Injector: &MemoryInjector{},
			},
			want: apperr.CodeASRModelInvalid,
		},
		{
			name: "audio",
			deps: Dependencies{
				SourceFactory: func() (audio.Source, error) { return nil, errors.New("connect failed") },
				Engine:        &FakeEngine{Text: "hello", IsLoaded: true},
				Injector:      &MemoryInjector{},
			},
			want: apperr.CodeAudioBackendUnavailable,
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
	if got, _ := app.post.Apply("hello", app.caseState); got != "hello" {
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
	next.ASR.Engine = asr.EngineWhisper
	next.ASR.Provider = asr.ProviderMetal
	next.ASR.ModelDir = filepath.Join(t.TempDir(), "other-model")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	engine := &FakeEngine{}
	app := New(ctx, cfg, Dependencies{
		Engine:         engine,
		ASRResolution:  asr.Resolution{Engine: asr.EngineSherpa, Provider: asr.ProviderCPU},
		ConfigReloader: func(context.Context) (config.Config, error) { return next, nil },
	})
	resp := app.HandleControl(ctx, control.NewRequest("reload_config", nil))
	if !resp.OK {
		t.Fatalf("reload failed: %+v", resp.Error)
	}
	status := app.Status(ctx)
	if !status.PendingRestart || status.LastWarning == nil || status.LastWarning.Code != "restart_required" {
		t.Fatalf("status = %+v, want pending restart warning", status)
	}
	if app.engine != engine || app.Status(ctx).ASR.ResolvedEngine != asr.EngineSherpa {
		t.Fatal("reload swapped the resolved ASR engine")
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

func TestControlRejectsAudioPreferenceControlledByConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[audio]\ndevice = \"configured\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	paths := config.PathsFor("darwin", config.PathEnvironment{HomeDir: t.TempDir(), UserConfigDir: t.TempDir(), UserCacheDir: t.TempDir(), UID: 501})
	cfg, err := config.LoadFor("darwin", paths, path)
	if err != nil {
		t.Fatal(err)
	}
	app := New(context.Background(), cfg, Dependencies{Preferences: preferences.NewMemoryStore()})
	resp := app.HandleControl(context.Background(), control.NewRequest("set_audio_device", map[string]any{"id": "other"}))
	if resp.OK || resp.Error == nil || resp.Error.Code != apperr.CodeControlledByConfig {
		t.Fatalf("response = %+v", resp)
	}
}

func TestControlSetAudioDeviceUsesPreferenceAndRecreatesSource(t *testing.T) {
	cfg := config.DefaultsFor("darwin", config.PlatformPaths{SocketPath: filepath.Join(t.TempDir(), "control.sock")})
	cfg.Focus.Enabled = false
	store := preferences.NewMemoryStore()
	selected := ""
	app := New(context.Background(), cfg, Dependencies{
		Preferences:   store,
		DeviceManager: staticDeviceManager{{ID: "device-1", Name: "Mic", Connected: true}},
		AudioSourceFactory: func(audioCfg config.Audio) (audio.Source, error) {
			selected = audioCfg.Device
			return &audio.ScriptedSource{SampleRate: 16000}, nil
		},
	})
	resp := app.HandleControl(context.Background(), control.NewRequest("set_audio_device", map[string]any{"id": "device-1"}))
	if !resp.OK {
		t.Fatalf("response = %+v", resp)
	}
	if selected != "device-1" || store.Values[preferences.KeySelectedAudioDeviceUID] != "device-1" {
		t.Fatalf("selected=%q preferences=%v", selected, store.Values)
	}
}

func TestControlInjectTextValidatesAndDoesNotPostProcess(t *testing.T) {
	cfg := config.Defaults()
	cfg.Focus.Enabled = false
	mem := &MemoryInjector{}
	app := New(context.Background(), cfg, Dependencies{Injector: mem})
	resp := app.HandleControl(context.Background(), control.NewRequest("inject_text", map[string]any{"text": "hello"}))
	if !resp.OK || len(mem.Texts()) != 1 || mem.Texts()[0] != "hello" {
		t.Fatalf("response=%+v texts=%v", resp, mem.Texts())
	}
	resp = app.HandleControl(context.Background(), control.NewRequest("inject_text", map[string]any{"text": "bad\x00text"}))
	if resp.OK || len(mem.Texts()) != 1 {
		t.Fatalf("NUL text was injected: response=%+v texts=%v", resp, mem.Texts())
	}
}

func TestControlPermissionsUsesPlatformSource(t *testing.T) {
	source := fakePermissionSource{snapshot: permissions.Snapshot{
		Microphone:    permissions.StateGranted,
		Accessibility: permissions.StateDenied,
	}}
	app := New(context.Background(), config.Defaults(), Dependencies{PermissionSource: source})
	resp := app.HandleControl(context.Background(), control.NewRequest("permissions", nil))
	if !resp.OK || resp.Data["microphone"] != permissions.StateGranted || resp.Status.Permissions == nil {
		t.Fatalf("response = %+v", resp)
	}
}

func TestControlRequestMicrophoneClassifiesDeniedStates(t *testing.T) {
	for _, state := range []permissions.State{permissions.Denied, permissions.Restricted} {
		t.Run(string(state), func(t *testing.T) {
			app := New(context.Background(), config.Defaults(), Dependencies{
				PermissionSource: fakePermissionSource{requestState: state},
			})
			resp := app.HandleControl(context.Background(), control.NewRequest("request_microphone_permission", nil))
			if resp.OK || resp.Error == nil || resp.Error.Code != apperr.CodePermissionMicrophoneDenied {
				t.Fatalf("response = %+v", resp)
			}
		})
	}
}

func TestControlSetLaunchAtLoginReturnsActualStatus(t *testing.T) {
	service := &fakeLoginItem{status: false}
	app := New(context.Background(), config.Defaults(), Dependencies{LoginItem: service})
	resp := app.HandleControl(context.Background(), control.NewRequest("set_launch_at_login", map[string]any{"enabled": true}))
	if !resp.OK || resp.Data["enabled"] != false {
		t.Fatalf("response = %+v", resp)
	}
	if service.requested == nil || !*service.requested {
		t.Fatalf("requested = %v", service.requested)
	}
}

type staticDeviceManager []audio.Device

func (m staticDeviceManager) Devices(context.Context) ([]audio.Device, error) {
	return append([]audio.Device(nil), m...), nil
}

type fakePermissionSource struct {
	snapshot     permissions.Snapshot
	requestState permissions.State
}

func (f fakePermissionSource) Snapshot(context.Context) (permissions.Snapshot, error) {
	return f.snapshot, nil
}

func (f fakePermissionSource) Request(_ context.Context, kind permissions.Kind) (permissions.State, error) {
	if f.requestState != "" {
		return f.requestState, nil
	}
	return permissions.StateGranted, nil
}

func (f fakePermissionSource) OpenSettings(context.Context, permissions.Kind) error { return nil }

type fakeLoginItem struct {
	status    bool
	requested *bool
}

func (f *fakeLoginItem) Status(context.Context) (bool, error) { return f.status, nil }

func (f *fakeLoginItem) SetEnabled(_ context.Context, enabled bool) error {
	f.requested = &enabled
	return nil
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
	if len(mem.Texts()) != 0 {
		t.Fatalf("discarded text was injected: %v", mem.Texts())
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
		if len(mem.Texts()) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(mem.Texts()) != 1 || mem.Texts()[0] != "committed " {
		t.Fatalf("committed text was not injected: %v", mem.Texts())
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
		if len(mem.Texts()) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(mem.Texts()) != 1 || mem.Texts()[0] != "flushed " {
		t.Fatalf("flushed text was not injected: %v", mem.Texts())
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
		if len(mem.Texts()) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(mem.Texts()) != 1 {
		t.Fatalf("expected injected text, got %v", mem.Texts())
	}
	if mem.Texts()[0] != "Hello, world! " {
		t.Fatalf("postprocessed text = %q", mem.Texts()[0])
	}
}

func TestCaseStateAdvancesAcrossSegments(t *testing.T) {
	cfg := config.Defaults()
	engine := &FakeEngine{Text: "First fragment", IsLoaded: true}
	mem := &MemoryInjector{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app := New(ctx, cfg, Dependencies{Engine: engine, Injector: mem})
	job := segmentJob{segment: asr.AudioSegment{ID: "first", Duration: time.Second}}

	app.handleSegment(ctx, job)
	engine.Text = "Jumped over."
	job.segment.ID = "second"
	app.handleSegment(ctx, job)

	want := []string{"first fragment ", "jumped over. "}
	if len(mem.Texts()) != len(want) || mem.Texts()[0] != want[0] || mem.Texts()[1] != want[1] {
		t.Fatalf("injected texts = %q, want %q", mem.Texts(), want)
	}
	app.mu.Lock()
	atBoundary := app.caseState.AtBoundary
	app.mu.Unlock()
	if !atBoundary {
		t.Fatal("sentence-ending continuation did not advance the boundary")
	}
}

func TestCaseStateDoesNotAdvanceOnInjectionFailure(t *testing.T) {
	cfg := config.Defaults()
	engine := &FakeEngine{Text: "First fragment", IsLoaded: true}
	mem := &MemoryInjector{err: errors.New("injection failed")}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app := New(ctx, cfg, Dependencies{Engine: engine, Injector: mem})
	job := segmentJob{segment: asr.AudioSegment{ID: "failed", Duration: time.Second}}

	app.handleSegment(ctx, job)
	app.mu.Lock()
	atBoundary := app.caseState.AtBoundary
	app.mu.Unlock()
	if !atBoundary {
		t.Fatal("failed injection advanced case state")
	}

	mem.SetError(nil)
	engine.Text = "hello there."
	job.segment.ID = "retry"
	app.handleSegment(ctx, job)
	if len(mem.Texts()) != 1 || mem.Texts()[0] != "Hello there. " {
		t.Fatalf("injected texts = %q, want boundary-capitalized retry", mem.Texts())
	}
}

func TestStartResetsCaseState(t *testing.T) {
	cfg := config.Defaults()
	cfg.Daemon.AutoStopAfterSilenceSeconds = 0
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app := New(ctx, cfg, Dependencies{
		Source:   &audio.ScriptedSource{SampleRate: 16000, Delay: time.Millisecond},
		Engine:   &FakeEngine{IsLoaded: true},
		Injector: &MemoryInjector{},
	})
	app.mu.Lock()
	app.caseState.AtBoundary = false
	app.mu.Unlock()

	if err := app.Start(ctx, api.ModeToggle); err != nil {
		t.Fatal(err)
	}
	defer app.Stop(ctx, false)
	app.mu.Lock()
	atBoundary := app.caseState.AtBoundary
	app.mu.Unlock()
	if !atBoundary {
		t.Fatal("Start did not reset case state")
	}
}

func TestDiscardedSessionCannotCommitCaseStateAfterRestart(t *testing.T) {
	cfg := config.Defaults()
	cfg.Daemon.AutoStopAfterSilenceSeconds = 0
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	release := make(chan struct{})
	mem := &blockingMemoryInjector{
		started: make(chan struct{}, 1),
		release: release,
	}
	app := New(ctx, cfg, Dependencies{
		Source:   &audio.ScriptedSource{SampleRate: 16000, Delay: time.Millisecond},
		Engine:   &FakeEngine{Text: "First fragment", IsLoaded: true},
		Injector: mem,
	})
	defer app.Stop(ctx, false)

	if err := app.Start(ctx, api.ModeOneshot); err != nil {
		t.Fatal(err)
	}
	app.queueSegment(asr.AudioSegment{ID: "stale", Duration: time.Second})
	select {
	case <-mem.started:
	case <-time.After(time.Second):
		t.Fatal("injector did not block")
	}
	if err := app.Stop(ctx, false); err != nil {
		t.Fatal(err)
	}
	if err := app.Start(ctx, api.ModeToggle); err != nil {
		t.Fatal(err)
	}
	close(release)

	deadline := time.Now().Add(time.Second)
	for {
		app.mu.Lock()
		pending := app.pendingASR
		state := app.caseState
		session := app.currentSession
		app.mu.Unlock()
		if pending == 0 {
			if session != 2 {
				t.Fatalf("current session = %d, want 2", session)
			}
			if state != (inject.CaseState{AtBoundary: true}) {
				t.Fatalf("case state = %+v, want reset boundary", state)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("stale ASR job did not finish")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestFocusChangeCancelsInjection(t *testing.T) {
	provider := &appFocusProvider{sameIDs: []string{"two"}}
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
		Focus:     provider,
	})
	if err := app.Start(ctx, api.ModeOneshot); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		st := app.Status(ctx)
		if st.LastError != nil && st.LastError.Code == "focus_changed" {
			if len(mem.Texts()) != 0 {
				t.Fatalf("text was injected despite focus change: %v", mem.Texts())
			}
			app.mu.Lock()
			atBoundary := app.caseState.AtBoundary
			app.mu.Unlock()
			if !atBoundary {
				t.Fatal("focus cancellation advanced case state")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("focus change was not recorded; status=%+v injected=%v", app.Status(ctx), mem.Texts())
}

func TestStartWithOptionsRejectsExpectedFocusPIDMismatch(t *testing.T) {
	cfg := config.Defaults()
	provider := &appFocusProvider{currentPID: 42}
	app := New(context.Background(), cfg, Dependencies{Focus: provider})
	err := app.StartWithOptions(context.Background(), StartOptions{
		Mode:             api.ModeOneshot,
		Origin:           StartOriginMenu,
		ExpectedFocusPID: 7,
	})
	if got := apperr.Code(err); got != apperr.CodeFocusChanged {
		t.Fatalf("code = %q, want %q (%v)", got, apperr.CodeFocusChanged, err)
	}
}

func TestFocusWarnAndTypeRecordsWarning(t *testing.T) {
	provider := &appFocusProvider{sameIDs: []string{"two", "two"}}
	cfg := config.Defaults()
	cfg.ASR.NumThreads = 1
	cfg.Focus.Policy = "warn_and_type"
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
		Focus:     provider,
	})
	if err := app.Start(ctx, api.ModeOneshot); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		st := app.Status(ctx)
		if st.LastWarning != nil && st.LastWarning.Code == "focus_changed" {
			if len(mem.Texts()) != 1 || mem.Texts()[0] != "secret " {
				t.Fatalf("text was not injected under warn_and_type: %v", mem.Texts())
			}
			if st.LastError != nil {
				t.Fatalf("last error = %+v, want nil", st.LastError)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("focus warning was not recorded; status=%+v injected=%v", app.Status(ctx), mem.Texts())
}

func TestInjectionFailureRetentionFollowsRedaction(t *testing.T) {
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
				Injector: &MemoryInjector{err: errors.New("injection failed")},
			})
			app.queueSegment(asr.AudioSegment{ID: "seg", Duration: time.Second})
			deadline := time.Now().Add(time.Second)
			for time.Now().Before(deadline) {
				st := app.Status(ctx)
				if st.LastError != nil {
					if st.LastError.Code != apperr.CodeInjectionFailed {
						t.Fatalf("error code = %s, want %s", st.LastError.Code, apperr.CodeInjectionFailed)
					}
					if st.LastUninjectedText != tc.wantStored {
						t.Fatalf("last uninjected text = %q, want %q", st.LastUninjectedText, tc.wantStored)
					}
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
			t.Fatalf("injection failure was not recorded: %+v", app.Status(ctx))
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
	// LastError is recorded on the capture path independently of the segmenter
	// reset above; poll briefly for it to propagate (races the reset under -race).
	deadline := time.After(time.Second)
	for {
		st := app.Status(ctx)
		if st.LastError != nil && st.LastError.Code == apperr.CodeAudioBackendUnavailable {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("unexpected status after capture error: %+v", st.LastError)
		case <-time.After(5 * time.Millisecond):
		}
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
			if st.LastError.Code != apperr.CodeRecognitionFailed {
				t.Fatalf("error code = %s, want %s", st.LastError.Code, apperr.CodeRecognitionFailed)
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

func (s *resetTrackingSegmenter) Name() string { return "fake" }

type errorSource struct {
	sampleRate int
}

type eventAudioSource struct {
	events chan audio.Event
}

func (*eventAudioSource) Start(context.Context) error                  { return nil }
func (*eventAudioSource) Pause(context.Context) error                  { return nil }
func (*eventAudioSource) Stop(context.Context) error                   { return nil }
func (*eventAudioSource) Read(context.Context, []float32) (int, error) { return 0, nil }
func (*eventAudioSource) Stats() audio.Stats                           { return audio.Stats{SampleRate: 16000} }
func (s *eventAudioSource) Events() <-chan audio.Event                 { return s.events }

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

type backendEngine struct {
	FakeEngine
	backend string
	gpu     bool
}

type loadErrorEngine struct {
	err error
}

func (e *loadErrorEngine) Name() string { return asr.EngineWhisper }

func (e *loadErrorEngine) Load(context.Context) error { return e.err }

func (e *loadErrorEngine) Close() error { return nil }

func (e *loadErrorEngine) Loaded() bool { return false }

func (e *loadErrorEngine) Transcribe(context.Context, asr.AudioSegment) (asr.Transcript, error) {
	return asr.Transcript{}, e.err
}

func (e *backendEngine) ActiveBackend() (string, bool) {
	return e.backend, e.gpu
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

type blockingMemoryInjector struct {
	MemoryInjector
	started chan struct{}
	release <-chan struct{}
}

func (m *blockingMemoryInjector) Inject(ctx context.Context, request inject.Request) error {
	select {
	case m.started <- struct{}{}:
	default:
	}
	select {
	case <-m.release:
		return m.MemoryInjector.Inject(ctx, request)
	case <-ctx.Done():
		return ctx.Err()
	}
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

type appFocusProvider struct {
	mu         sync.Mutex
	next       uint64
	currentPID int
	sameIDs    []string
}

func (*appFocusProvider) Backend() string                 { return "test" }
func (*appFocusProvider) Available(context.Context) error { return nil }
func (p *appFocusProvider) Release(focus.Target)          {}
func (p *appFocusProvider) Current(context.Context) (focus.Target, error) {
	return p.target("one"), nil
}
func (p *appFocusProvider) Same(_ context.Context, target focus.Target) (focus.Target, bool, error) {
	p.mu.Lock()
	id := target.StableID
	if len(p.sameIDs) > 0 {
		id = p.sameIDs[0]
		p.sameIDs = p.sameIDs[1:]
	}
	p.mu.Unlock()
	current := p.target(id)
	return current, current.StableID == target.StableID, nil
}
func (p *appFocusProvider) target(id string) focus.Target {
	p.mu.Lock()
	p.next++
	token := p.next
	p.mu.Unlock()
	return focus.Target{Backend: "test", StableID: id, PID: p.currentPID, Token: token}
}
