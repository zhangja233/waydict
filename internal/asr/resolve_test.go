package asr

import (
	"context"
	"errors"
	"testing"
)

func TestResolveInjectedWhisperProviderPreference(t *testing.T) {
	tests := []struct {
		name       string
		configured string
		preferred  string
		want       string
		wantProbe  bool
	}{
		{name: "Darwin defaults Metal", preferred: ProviderMetal, want: ProviderMetal, wantProbe: true},
		{name: "Linux defaults Vulkan", preferred: ProviderVulkan, want: ProviderVulkan, wantProbe: true},
		{name: "CPU preference", preferred: ProviderCPU, want: ProviderCPU},
		{name: "explicit CPU overrides Metal", configured: ProviderCPU, preferred: ProviderMetal, want: ProviderCPU},
		{name: "explicit Metal overrides CPU", configured: ProviderMetal, preferred: ProviderCPU, want: ProviderMetal, wantProbe: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			probeCalls := 0
			constructedProvider := ""
			engine, resolution, err := Resolve(EngineWhisper, tt.configured, 0, ResolverDeps{
				PreferredWhisperProvider: tt.preferred,
				NumThreads:               2,
				NewSherpa:                func() Engine { return &resolveTestEngine{name: EngineSherpa} },
				NewWhisper: func(_ string, provider string, _ int, _ int) (Engine, error) {
					constructedProvider = provider
					return &resolveTestEngine{name: EngineWhisper}, nil
				},
				ProbeAccelerator: func(provider string, device int) (Accelerator, error) {
					probeCalls++
					return Accelerator{Provider: provider, Device: device, Name: "test device"}, nil
				},
				WhisperModelPath: func() (string, error) { return "/model.ggml", nil },
			})
			if err != nil {
				t.Fatal(err)
			}
			if engine.Name() != EngineWhisper || resolution.Provider != tt.want || constructedProvider != tt.want {
				t.Fatalf("engine=%s resolution=%+v constructed provider=%q", engine.Name(), resolution, constructedProvider)
			}
			if (probeCalls == 1) != tt.wantProbe {
				t.Fatalf("probe calls = %d, wantProbe %t", probeCalls, tt.wantProbe)
			}
		})
	}
}

func TestResolveAutoPreflightFallsBackToSherpa(t *testing.T) {
	sherpa := &resolveTestEngine{name: EngineSherpa}
	engine, resolution, err := Resolve(EngineAuto, "", 0, ResolverDeps{
		PreferredWhisperProvider: ProviderMetal,
		NewSherpa:                func() Engine { return sherpa },
		WhisperModelPath:         func() (string, error) { return "", errors.New("missing") },
	})
	if err != nil {
		t.Fatal(err)
	}
	if engine != sherpa || resolution.Engine != EngineSherpa || resolution.Provider != ProviderCPU || resolution.FallbackReason == "" {
		t.Fatalf("engine=%T resolution=%+v, want sherpa fallback", engine, resolution)
	}
}

func TestConfirmWhisperBackendDecisionTable(t *testing.T) {
	metal := Resolution{Engine: EngineWhisper, Provider: ProviderMetal, GPUName: "provisional"}
	cpu := Resolution{Engine: EngineWhisper, Provider: ProviderCPU}
	tests := []struct {
		name               string
		configuredEngine   string
		configuredProvider string
		resolution         Resolution
		report             BackendReport
		wantProvider       string
		wantGPUName        string
		wantAction         BackendConfirmationAction
	}{
		{name: "auto empty confirms Metal", configuredEngine: EngineAuto, resolution: metal, report: BackendReport{Provider: ProviderMetal, DeviceName: "Apple M4", GPU: true}, wantProvider: ProviderMetal, wantGPUName: "Apple M4", wantAction: BackendKeep},
		{name: "auto empty CPU falls back", configuredEngine: EngineAuto, resolution: metal, report: BackendReport{Provider: ProviderCPU, DeviceName: "CPU"}, wantProvider: ProviderCPU, wantAction: BackendFallback},
		{name: "auto empty other GPU falls back", configuredEngine: EngineAuto, resolution: metal, report: BackendReport{Provider: ProviderVulkan, DeviceName: "other", GPU: true}, wantProvider: ProviderCPU, wantAction: BackendFallback},
		{name: "auto explicit Metal CPU unavailable", configuredEngine: EngineAuto, configuredProvider: ProviderMetal, resolution: metal, report: BackendReport{Provider: ProviderCPU, DeviceName: "CPU"}, wantProvider: ProviderCPU, wantAction: BackendUnavailable},
		{name: "forced empty Metal CPU unavailable", configuredEngine: EngineWhisper, resolution: metal, report: BackendReport{Provider: ProviderCPU, DeviceName: "CPU"}, wantProvider: ProviderCPU, wantAction: BackendUnavailable},
		{name: "forced explicit Metal unconfirmed unavailable", configuredEngine: EngineWhisper, configuredProvider: ProviderMetal, resolution: metal, wantProvider: ProviderCPU, wantAction: BackendUnavailable},
		{name: "auto CPU expects CPU", configuredEngine: EngineAuto, configuredProvider: ProviderCPU, resolution: cpu, report: BackendReport{Provider: ProviderCPU, DeviceName: "CPU"}, wantProvider: ProviderCPU, wantAction: BackendKeep},
		{name: "forced CPU expects CPU", configuredEngine: EngineWhisper, configuredProvider: ProviderCPU, resolution: cpu, report: BackendReport{Provider: ProviderCPU, DeviceName: "CPU"}, wantProvider: ProviderCPU, wantAction: BackendKeep},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolution, action := ConfirmWhisperBackend(tt.configuredEngine, tt.configuredProvider, tt.resolution, tt.report)
			if resolution.Provider != tt.wantProvider || resolution.GPUName != tt.wantGPUName || action != tt.wantAction {
				t.Fatalf("confirmation = (%+v, %d), want provider=%q gpu=%q action=%d", resolution, action, tt.wantProvider, tt.wantGPUName, tt.wantAction)
			}
		})
	}
}

type resolveTestEngine struct {
	name   string
	loaded bool
}

func (e *resolveTestEngine) Name() string { return e.name }
func (e *resolveTestEngine) Load(context.Context) error {
	e.loaded = true
	return nil
}
func (e *resolveTestEngine) Close() error {
	e.loaded = false
	return nil
}
func (e *resolveTestEngine) Loaded() bool { return e.loaded }
func (e *resolveTestEngine) Transcribe(context.Context, AudioSegment) (Transcript, error) {
	return Transcript{}, nil
}

func TestResolveRemoteWrapsTheConfiguredFallback(t *testing.T) {
	tests := []struct {
		name         string
		fallback     string
		wantFallback string
		wantErr      bool
	}{
		{name: "sherpa fallback", fallback: EngineSherpa, wantFallback: EngineSherpa},
		{name: "no fallback", fallback: FallbackNone},
		{name: "unknown fallback", fallback: EngineWhisper, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var wrapped Engine
			deps := ResolverDeps{
				NewSherpa:      func() Engine { return &resolveTestEngine{name: EngineSherpa} },
				RemoteFallback: tc.fallback,
				NewRemote: func(fallback Engine) (Engine, error) {
					wrapped = fallback
					return &resolveTestEngine{name: EngineRemote}, nil
				},
			}
			engine, resolution, err := Resolve(EngineRemote, "", 0, deps)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Resolve() error = %v, wantErr %t", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if engine.Name() != EngineRemote || resolution.Engine != EngineRemote || resolution.Provider != ProviderRemote {
				t.Fatalf("resolution = %+v, engine = %q", resolution, engine.Name())
			}
			if tc.wantFallback == "" {
				if wrapped != nil {
					t.Fatalf("wrapped %q, want no fallback", wrapped.Name())
				}
				return
			}
			if wrapped == nil || wrapped.Name() != tc.wantFallback {
				t.Fatalf("wrapped %v, want %q", wrapped, tc.wantFallback)
			}
		})
	}
}

// auto is the GPU-or-CPU choice; reaching another host is never implicit.
func TestResolveAutoNeverPicksRemote(t *testing.T) {
	deps := ResolverDeps{
		NewSherpa:      func() Engine { return &resolveTestEngine{name: EngineSherpa} },
		RemoteFallback: EngineSherpa,
		NewRemote: func(Engine) (Engine, error) {
			t.Fatal("auto resolved to the remote engine")
			return nil, nil
		},
	}
	_, resolution, err := Resolve(EngineAuto, "", 0, deps)
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Engine != EngineSherpa {
		t.Fatalf("auto resolved to %q, want %q", resolution.Engine, EngineSherpa)
	}
}
