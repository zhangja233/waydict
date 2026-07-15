//go:build darwin && coreaudio && cgo

package platform

import (
	"fmt"

	"waydict/internal/apperr"
	"waydict/internal/audio"
	"waydict/internal/audio/coreaudio"
	"waydict/internal/config"
)

func Current() Services {
	services := currentDarwinServices()
	services.Capabilities.AudioBackends = []string{"coreaudio"}
	services.NewAudio = func(cfg config.Audio) (audio.Source, error) {
		switch cfg.Backend {
		case "", "auto", "coreaudio":
			return coreaudio.New(cfg)
		default:
			return nil, apperr.New(apperr.CodeAudioBackendUnavailable, "select macOS audio backend", fmt.Errorf("unsupported backend %q", cfg.Backend))
		}
	}
	services.Devices = coreaudio.Manager()
	return services
}
