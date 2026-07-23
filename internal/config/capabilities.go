package config

import "runtime"

type CapabilitySet struct {
	Platform         string
	AudioBackends    []string
	InjectionEngines []string
	FocusBackends    []string
	WhisperProviders []string
	SherpaProviders  []string
	HotkeyAllowed    bool
}

type Capabilities = CapabilitySet

func CurrentCapabilitySet() CapabilitySet {
	return CapabilitySetFor(runtime.GOOS)
}

func PreferredWhisperProviderFor(platform string) string {
	providers := CapabilitySetFor(platform).WhisperProviders
	for _, provider := range providers {
		if provider != "cpu" {
			return provider
		}
	}
	return preferredProvider(providers)
}

func CapabilitySetFor(platform string) CapabilitySet {
	switch platform {
	case "darwin":
		return CapabilitySet{
			Platform:         platform,
			AudioBackends:    []string{"coreaudio"},
			InjectionEngines: []string{"quartz"},
			FocusBackends:    []string{"accessibility"},
			WhisperProviders: []string{"metal", "cpu"},
			SherpaProviders:  []string{"cpu"},
			HotkeyAllowed:    true,
		}
	case "linux":
		return CapabilitySet{
			Platform:         platform,
			AudioBackends:    []string{"pipewire"},
			InjectionEngines: []string{"wtype"},
			FocusBackends:    []string{"sway"},
			// PreferredWhisperProviderFor takes the first non-cpu entry, so vulkan stays
			// the default. CUDA is offered but not preferred: it has not measured faster
			// for Whisper's encoder, which dominates short dictation clips. Set
			// asr.provider explicitly to override.
			WhisperProviders: []string{"cpu", "vulkan", "cuda"},
			SherpaProviders:  []string{"cpu"},
		}
	default:
		return CapabilitySet{Platform: platform}
	}
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
