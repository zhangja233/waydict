//go:build darwin

package platform

import (
	"waydict/internal/asr"
	"waydict/internal/buildinfo"
	"waydict/internal/macos/appkit"
	macosloginitem "waydict/internal/macos/loginitem"
	macospermissions "waydict/internal/macos/permissions"
	macosuserdefaults "waydict/internal/macos/userdefaults"
)

func currentDarwinServices() Services {
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
	services.Preferences = macosuserdefaults.New()
	services.AppActivation = appkit.NewActivator()
	return services
}
