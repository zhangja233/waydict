//go:build linux && whispercpp && cgo

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

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
	probeGPUHook = probeGPU
}

// probeGPU gates each provider on its own runtime. Probing whichever GPU stack
// happens to be installed would let a forced provider pass on the strength of a
// different one — a Vulkan ICD says nothing about CUDA being usable.
func probeGPU(provider string) (string, error) {
	switch provider {
	case asr.ProviderCUDA:
		return probeCUDAGPU()
	case asr.ProviderVulkan, "":
		return probeVulkanGPU()
	default:
		return "", fmt.Errorf("unsupported accelerator provider %q on linux", provider)
	}
}

// probeCUDAGPU checks the driver's character devices rather than libcuda.so: the
// library lives in the driver tree (/run/opengl-driver/lib on NixOS) and is not
// reliably on the loader path, whereas /dev/nvidiactl plus a numbered node means
// the kernel modules are loaded and bound to hardware.
func probeCUDAGPU() (string, error) {
	if _, err := os.Stat("/dev/nvidiactl"); err != nil {
		return "", errors.New("no /dev/nvidiactl; nvidia kernel modules not loaded")
	}
	nodes, _ := filepath.Glob("/dev/nvidia[0-9]*")
	for _, node := range nodes {
		if f, err := os.OpenFile(node, os.O_RDWR, 0); err == nil {
			f.Close()
			return "cuda device via " + filepath.Base(node), nil
		}
	}
	return "", errors.New("no accessible nvidia device node")
}

// probeVulkanGPU is a cheap pre-init gate: an installed Vulkan ICD plus a DRM
// render node make a GPU attempt worth the model load. The engine's post-Load
// backend report stays authoritative; false positives downgrade there.
func probeVulkanGPU() (string, error) {
	icdDirs := []string{
		"/run/opengl-driver/share/vulkan/icd.d", // NixOS
		"/usr/share/vulkan/icd.d",
		"/etc/vulkan/icd.d",
	}
	icd := ""
	for _, dir := range icdDirs {
		matches, _ := filepath.Glob(filepath.Join(dir, "*.json"))
		if len(matches) > 0 {
			icd = dir
			break
		}
	}
	if icd == "" {
		return "", errors.New("no vulkan ICD found")
	}
	nodes, _ := filepath.Glob("/dev/dri/renderD*")
	for _, node := range nodes {
		if f, err := os.OpenFile(node, os.O_RDWR, 0); err == nil {
			f.Close()
			return "vulkan device via " + filepath.Base(node), nil
		}
	}
	return "", errors.New("no accessible DRM render node")
}
