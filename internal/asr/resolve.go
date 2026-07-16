package asr

import "fmt"

// Engine names and providers accepted by config and reported in Resolution.
const (
	EngineAuto    = "auto"
	EngineSherpa  = "sherpa-onnx"
	EngineWhisper = "whisper-cpp"

	ProviderCPU    = "cpu"
	ProviderMetal  = "metal"
	ProviderVulkan = "vulkan"
)

// Resolution records how the engine was chosen, for startup logs, status, and doctor.
// GPUName comes from the pre-construction probe and is provisional: whisper.cpp can
// still fall back to CPU during model load, so callers must confirm against the
// engine's post-Load backend report before claiming GPU in user-facing output.
type Resolution struct {
	Engine         string
	Provider       string
	Device         int    // GPU device index; meaningful only when Provider != cpu
	GPUName        string // probed device name; "" on CPU
	FallbackReason string // set when auto resolved away from the GPU engine
}

type BackendReport struct {
	Provider   string
	DeviceName string
	GPU        bool
}

// BackendReporter reports the backend selected after Load.
type BackendReporter interface {
	ActiveBackend() BackendReport
}

type BackendConfirmationAction uint8

const (
	BackendKeep BackendConfirmationAction = iota
	BackendFallback
	BackendUnavailable
)

// ResolverDeps supplies constructors and probes so resolution stays testable and
// free of cgo. NewWhisper is nil when built without the whispercpp tag.
type ResolverDeps struct {
	PreferredWhisperProvider string
	NumThreads               int
	NewSherpa                func() Engine
	NewWhisper               func(modelPath, provider string, device, threads int) (Engine, error)
	ProbeAccelerator         func(provider string, device int) (Accelerator, error)
	WhisperModelPath         func() (string, error) // installed ggml model, or why not
}

// Resolve picks the engine per the configured asr.engine value.
//
// Forced engines fail loudly: whisper-cpp with a vulkan provider errors when the
// GPU or model is unavailable rather than silently degrading. auto never errors:
// any missing precondition falls back to sherpa CPU with FallbackReason set.
// If an auto-resolved whisper engine later fails Load, the caller re-resolves
// with EngineSherpa; explicit engines get no such retry.
func Resolve(engine, provider string, device int, deps ResolverDeps) (Engine, Resolution, error) {
	switch engine {
	case EngineSherpa:
		return deps.NewSherpa(), Resolution{Engine: EngineSherpa, Provider: ProviderCPU}, nil

	case EngineWhisper:
		eng, res, err := resolveWhisper(provider, device, deps)
		if err != nil {
			return nil, Resolution{}, err
		}
		return eng, res, nil

	case EngineAuto:
		eng, res, err := resolveWhisper(provider, device, deps)
		if err != nil {
			return deps.NewSherpa(), Resolution{
				Engine:         EngineSherpa,
				Provider:       ProviderCPU,
				FallbackReason: err.Error(),
			}, nil
		}
		return eng, res, nil

	default:
		return nil, Resolution{}, fmt.Errorf("unknown asr.engine %q", engine)
	}
}

func resolveWhisper(provider string, device int, deps ResolverDeps) (Engine, Resolution, error) {
	if deps.NewWhisper == nil {
		return nil, Resolution{}, fmt.Errorf("whisper-cpp engine not built in (whispercpp tag)")
	}
	if deps.WhisperModelPath == nil {
		return nil, Resolution{}, fmt.Errorf("whisper model lookup not wired")
	}
	modelPath, err := deps.WhisperModelPath()
	if err != nil {
		return nil, Resolution{}, fmt.Errorf("whisper model unavailable: %w", err)
	}
	if provider == "" {
		provider = deps.PreferredWhisperProvider
	}
	if provider == "" {
		provider = ProviderCPU
	}
	res := Resolution{Engine: EngineWhisper, Provider: provider, Device: device}
	if provider != ProviderCPU {
		if deps.ProbeAccelerator == nil {
			return nil, Resolution{}, fmt.Errorf("%s accelerator probe not wired", provider)
		}
		accelerator, err := deps.ProbeAccelerator(provider, device)
		if err != nil {
			return nil, Resolution{}, fmt.Errorf("no usable %s accelerator: %w", provider, err)
		}
		res.GPUName = accelerator.Name
	}
	eng, err := deps.NewWhisper(modelPath, provider, device, deps.NumThreads)
	if err != nil {
		return nil, Resolution{}, fmt.Errorf("whisper-cpp init: %w", err)
	}
	return eng, res, nil
}

// ConfirmWhisperBackend applies the post-load provider policy without cgo.
func ConfirmWhisperBackend(configuredEngine, configuredProvider string, resolution Resolution, report BackendReport) (Resolution, BackendConfirmationAction) {
	if resolution.Engine != EngineWhisper {
		return resolution, BackendKeep
	}
	if resolution.Provider == ProviderCPU {
		resolution.GPUName = ""
		return resolution, BackendKeep
	}
	if resolution.Provider == ProviderMetal {
		if report.GPU && report.Provider == ProviderMetal {
			resolution.GPUName = report.DeviceName
			return resolution, BackendKeep
		}
		resolution.Provider = ProviderCPU
		resolution.GPUName = ""
		switch {
		case configuredProvider == ProviderMetal:
			return resolution, BackendUnavailable
		case configuredEngine == EngineWhisper && configuredProvider == "":
			return resolution, BackendUnavailable
		case configuredEngine == EngineAuto && configuredProvider == "":
			return resolution, BackendFallback
		default:
			return resolution, BackendUnavailable
		}
	}
	if report.GPU {
		resolution.GPUName = report.DeviceName
		return resolution, BackendKeep
	}
	resolution.Provider = ProviderCPU
	resolution.GPUName = ""
	return resolution, BackendKeep
}
