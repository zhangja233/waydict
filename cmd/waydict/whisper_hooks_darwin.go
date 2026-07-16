//go:build darwin && whispercpp && cgo

package main

import (
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
	probeGPUHook = func() (string, error) {
		return "Metal device 0", nil
	}
}
