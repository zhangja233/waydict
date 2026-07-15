//go:build darwin

package platform

import (
	"waydict/internal/asr"
	"waydict/internal/buildinfo"
	"waydict/internal/config"
	"waydict/internal/focus"
	macosfocus "waydict/internal/focus/macos"
	"waydict/internal/inject"
	"waydict/internal/inject/quartz"
	"waydict/internal/macos/appkit"
	macosloginitem "waydict/internal/macos/loginitem"
	macospermissions "waydict/internal/macos/permissions"
	macosuserdefaults "waydict/internal/macos/userdefaults"
)

func currentDarwinServices() Services {
	services := unavailableServices("darwin")
	services.Capabilities.Host = "macos_app"
	services.Capabilities.InjectionBackends = []string{"quartz"}
	services.Capabilities.FocusBackends = []string{"accessibility"}
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
	services.NewInjector = func(cfg config.Injection) inject.Injector {
		switch cfg.Engine {
		case "", "auto", "quartz":
			return quartz.New(cfg)
		default:
			return inject.Unavailable{Name: cfg.Engine}
		}
	}
	services.NewFocus = func(cfg config.Focus) focus.Provider {
		switch cfg.Backend {
		case "", "auto", "accessibility":
			return macosfocus.New()
		default:
			return unavailableFocus{backend: cfg.Backend}
		}
	}
	return services
}
