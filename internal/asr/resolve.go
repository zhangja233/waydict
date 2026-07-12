package asr

import "fmt"

// Engine names and providers accepted by config and reported in Resolution.
const (
	EngineAuto    = "auto"
	EngineSherpa  = "sherpa-onnx"
	EngineWhisper = "whisper-cpp"

	ProviderCPU    = "cpu"
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

// BackendReporter is optionally implemented by engines that can report which
// backend the native library actually selected after Load. Callers use it to
// confirm (or refute) a provisional GPU Resolution before surfacing it.
type BackendReporter interface {
	ActiveBackend() (name string, gpu bool)
}

// ResolverDeps supplies constructors and probes so resolution stays testable and
// free of cgo. NewWhisper is nil when built without the whispercpp tag.
type ResolverDeps struct {
	NewSherpa        func() Engine
	NewWhisper       func(modelPath string, device int, useGPU bool) (Engine, error)
	ProbeGPU         func() (name string, err error)
	WhisperModelPath func() (string, error) // installed ggml model, or why not
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
		eng, res, err := resolveWhisper(ProviderVulkan, device, deps)
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
	res := Resolution{Engine: EngineWhisper, Provider: provider, Device: device}
	useGPU := provider != ProviderCPU
	if useGPU {
		if deps.ProbeGPU == nil {
			return nil, Resolution{}, fmt.Errorf("gpu probe not wired")
		}
		name, err := deps.ProbeGPU()
		if err != nil {
			return nil, Resolution{}, fmt.Errorf("no usable GPU: %w", err)
		}
		res.GPUName = name
	}
	eng, err := deps.NewWhisper(modelPath, device, useGPU)
	if err != nil {
		return nil, Resolution{}, fmt.Errorf("whisper-cpp init: %w", err)
	}
	return eng, res, nil
}
