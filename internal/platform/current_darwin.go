//go:build darwin

package platform

import (
	"waydict/internal/asr"
	"waydict/internal/buildinfo"
	"waydict/internal/macos/appkit"
	macosloginitem "waydict/internal/macos/loginitem"
	macospermissions "waydict/internal/macos/permissions"
)

func Current() Services {
	services := unavailableServices("darwin")
	services.Capabilities.Host = "macos_app"
	if buildinfo.WhisperEnabled {
		services.Capabilities.WhisperProviders = []string{asr.ProviderMetal, asr.ProviderCPU}
	}
	if buildinfo.SherpaEnabled {
		services.Capabilities.SherpaProviders = []string{asr.ProviderCPU}
	}
	services.Permissions = macospermissions.New()
	services.LoginItem = macosloginitem.New()
	services.AppActivation = appkit.NewActivator()
	return services
}
