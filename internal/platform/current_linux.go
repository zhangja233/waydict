//go:build linux

package platform

import (
	"waydict/internal/asr"
	"waydict/internal/audio"
	"waydict/internal/audio/pipewire"
	"waydict/internal/buildinfo"
	"waydict/internal/config"
	"waydict/internal/focus"
	swayfocus "waydict/internal/focus/sway"
	"waydict/internal/inject"
	"waydict/internal/swayipc"
)

func Current() Services {
	var audioBackends, whisperProviders, sherpaProviders []string
	if buildinfo.PipeWireEnabled {
		audioBackends = []string{"pipewire"}
	}
	if buildinfo.WhisperEnabled {
		whisperProviders = []string{asr.ProviderCPU, asr.ProviderVulkan, asr.ProviderCUDA}
	}
	if buildinfo.SherpaEnabled {
		sherpaProviders = []string{asr.ProviderCPU}
	}
	return Services{
		Capabilities: Capabilities{
			OS:                "linux",
			Host:              "daemon",
			AudioBackends:     audioBackends,
			InjectionBackends: []string{"wtype"},
			FocusBackends:     []string{"sway"},
			WhisperProviders:  whisperProviders,
			SherpaProviders:   sherpaProviders,
		},
		NewAudio: func(cfg config.Audio) (audio.Source, error) {
			return pipewire.New(cfg)
		},
		NewInjector: func(cfg config.Injection) inject.Injector {
			return inject.NewWtype(cfg)
		},
		NewFocus: func(cfg config.Focus) focus.Provider {
			return swayfocus.New(swayipc.New(cfg.Socket))
		},
	}
}
