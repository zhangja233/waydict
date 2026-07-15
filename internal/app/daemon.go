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
			Name:             services.Capabilities.OS,
			NewSource:        services.NewAudio,
			NewInjector:      services.NewInjector,
			NewFocusProvider: services.NewFocus,
			PermissionSource: services.Permissions,
			DeviceManager:    services.Devices,
		},
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
