//go:build whispercpp && cgo

package main

import (
	"errors"
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
	probeGPUHook = probeVulkanGPU
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
