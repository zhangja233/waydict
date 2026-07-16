//go:build darwin && whispercpp && cgo

package main

import (
	"fmt"

	"waydict/internal/app"
	"waydict/internal/asr"
	"waydict/internal/asr/whispercpp"
)

var appWhisperFactory app.WhisperFactory = func(modelPath, provider string, device, threads int) (asr.Engine, error) {
	return whispercpp.New(whispercpp.Config{
		ModelPath:  modelPath,
		Device:     device,
		NumThreads: threads,
		UseGPU:     provider != asr.ProviderCPU,
	})
}

var appAcceleratorProbe = func(provider string, device int) (asr.Accelerator, error) {
	if provider != asr.ProviderMetal {
		return asr.Accelerator{}, fmt.Errorf("unsupported macOS accelerator provider %q", provider)
	}
	if device != 0 {
		return asr.Accelerator{}, fmt.Errorf("Metal supports only device 0")
	}
	return asr.Accelerator{Provider: provider, Device: device, Name: "Metal device 0"}, nil
}
