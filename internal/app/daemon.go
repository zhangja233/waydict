package app

import (
	"context"

	"waydict/internal/asr"
	"waydict/internal/config"
	"waydict/internal/platform"
)

type DaemonOptions struct {
	ConfigPath       string
	LogLevelOverride string
	NewWhisper       func(modelPath string, device, threads int, useGPU bool) (asr.Engine, error)
	ProbeGPU         func() (string, error)
}

func RunDaemon(ctx context.Context, cfg config.Config) error {
	return RunDaemonWithOptions(ctx, cfg, DaemonOptions{})
}

func RunDaemonWithOptions(ctx context.Context, cfg config.Config, opts DaemonOptions) error {
	services := platform.Current()
	runtimeOpts := RuntimeOptions{
		ConfigPath:       opts.ConfigPath,
		LogLevelOverride: opts.LogLevelOverride,
		Platform: PlatformDependencies{
			Name: services.Capabilities.OS,
			Capabilities: ControlCapabilities{
				Platform:         services.Capabilities.OS,
				Host:             services.Capabilities.Host,
				AudioBackends:    services.Capabilities.AudioBackends,
				InjectionEngines: services.Capabilities.InjectionBackends,
				FocusBackends:    services.Capabilities.FocusBackends,
				WhisperProviders: services.Capabilities.WhisperProviders,
				SherpaProviders:  services.Capabilities.SherpaProviders,
				HotkeyAvailable:  services.Capabilities.HotkeyAvailable,
			},
			NewSource:        services.NewAudio,
			NewInjector:      services.NewInjector,
			NewFocusProvider: services.NewFocus,
			PermissionSource: services.Permissions,
			DeviceManager:    services.Devices,
			Preferences:      services.Preferences,
			Hotkey:           services.Hotkey,
			LoginItem:        services.LoginItem,
		},
	}
	if services.AppActivation != nil {
		runtimeOpts.Platform.HostActions.Activate = func(ctx context.Context) error {
			return services.AppActivation.ActivateBundle(ctx, "io.github.zhangja233.waydict")
		}
	}
	if opts.NewWhisper != nil {
		runtimeOpts.NewWhisper = func(modelPath, provider string, device, threads int) (asr.Engine, error) {
			return opts.NewWhisper(modelPath, device, threads, provider != asr.ProviderCPU)
		}
	}
	if opts.ProbeGPU != nil {
		runtimeOpts.ProbeAccelerator = func(provider string, device int) (asr.Accelerator, error) {
			name, err := opts.ProbeGPU()
			return asr.Accelerator{Provider: provider, Device: device, Name: name}, err
		}
	}
	runtime, err := NewRuntime(ctx, cfg, runtimeOpts)
	if err != nil {
		return err
	}
	defer runtime.Close(context.Background())
	return runtime.Serve(ctx)
}
