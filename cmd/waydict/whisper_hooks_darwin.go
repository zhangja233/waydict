//go:build darwin && whispercpp && cgo

package main

import (
	"fmt"

	"waydict/internal/asr"
	"waydict/internal/asr/whispercpp"
)

func init() {
	newWhisperEngineHook = func(modelPath string, device, threads int, useGPU bool) (asr.Engine, error) {
		return whispercpp.New(whispercpp.Config{
			ModelPath:  modelPath,
			Device:     device,
			NumThreads: threads,
			UseGPU:     useGPU,
		})
	}
	probeGPUHook = func(provider string) (string, error) {
		// Metal is the only accelerator here; CUDA is not a macOS provider at all.
		if provider != asr.ProviderMetal && provider != "" {
			return "", fmt.Errorf("unsupported accelerator provider %q on darwin", provider)
		}
		return "Metal device 0", nil
	}
}
