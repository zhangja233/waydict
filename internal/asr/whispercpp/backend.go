package whispercpp

import (
	"strings"

	"waydict/internal/asr"
)

type backendDetector struct {
	devices map[string]string
	report  asr.BackendReport
	// Set once ggml_cuda_init announces itself; the per-device lines that follow
	// are bare "Device N: ..." and would otherwise be too generic to claim.
	cudaInit bool
}

func (d *backendDetector) observe(text string) {
	for _, line := range strings.Split(text, "\n") {
		d.observeLine(strings.TrimSpace(line))
	}
}

func (d *backendDetector) observeLine(line string) {
	if line == "" {
		return
	}
	const vulkanPrefix = "ggml_vulkan: "
	if rest, ok := strings.CutPrefix(line, vulkanPrefix); ok {
		if index, detail, ok := strings.Cut(rest, " = "); ok {
			name, _, _ := strings.Cut(detail, " | ")
			if index != "" && name != "" {
				if d.devices == nil {
					d.devices = make(map[string]string)
				}
				d.devices["Vulkan"+index] = name
			}
		}
	}

	// CUDA prints a header then one indented line per GPU:
	//   ggml_cuda_init: found 1 CUDA devices (Total VRAM: 15846 MiB):
	//     Device 0: NVIDIA GeForce RTX 5060 Ti, compute capability 12.0, VMM: yes
	if strings.HasPrefix(line, "ggml_cuda_init:") {
		d.cudaInit = true
		return
	}
	if d.cudaInit {
		if rest, ok := strings.CutPrefix(line, "Device "); ok {
			if index, detail, ok := strings.Cut(rest, ": "); ok {
				name, _, _ := strings.Cut(detail, ",")
				name = strings.TrimSpace(name)
				if index != "" && name != "" {
					if d.devices == nil {
						d.devices = make(map[string]string)
					}
					d.devices["CUDA"+index] = name
				}
			}
			return
		}
	}
	if marker := "GPU name:"; strings.Contains(line, marker) && strings.Contains(line, "ggml_metal") {
		detail := strings.TrimSpace(line[strings.Index(line, marker)+len(marker):])
		backend, device := metalDevice(detail)
		if backend != "" {
			if d.devices == nil {
				d.devices = make(map[string]string)
			}
			d.devices[backend] = device
			if d.report.Provider == asr.ProviderMetal && (d.report.DeviceName == backend || d.report.DeviceName == "Metal") {
				d.report.DeviceName = device
			}
		}
	}
	if marker := "ggml_metal_init: picking default device:"; strings.Contains(line, marker) {
		device := strings.TrimSpace(line[strings.Index(line, marker)+len(marker):])
		if device != "" && d.report.Provider == asr.ProviderMetal {
			d.report.DeviceName = device
		}
	}

	const usingPrefix = "whisper_backend_init_gpu: using "
	if rest, ok := strings.CutPrefix(line, usingPrefix); ok {
		if backend, ok := strings.CutSuffix(rest, " backend"); ok && backend != "" {
			provider := backendProvider(backend)
			if provider != "" {
				d.report = asr.BackendReport{Provider: provider, DeviceName: backend, GPU: true}
				if name := d.devices[backend]; name != "" {
					d.report.DeviceName = name
				}
			}
		}
		return
	}

	if strings.Contains(line, "whisper_backend_init_gpu: no GPU found") ||
		strings.Contains(line, "whisper_backend_init_gpu: failed to initialize") ||
		(strings.Contains(line, "whisper_model_load:") && strings.Contains(line, "CPU total size")) {
		d.report = asr.BackendReport{Provider: asr.ProviderCPU, DeviceName: "CPU"}
	}
}

func (d *backendDetector) backend() asr.BackendReport {
	return d.report
}

func backendProvider(name string) string {
	switch {
	case name == "Metal", strings.HasPrefix(name, "MTL"):
		return asr.ProviderMetal
	case strings.HasPrefix(name, "Vulkan"):
		return asr.ProviderVulkan
	case strings.HasPrefix(name, "CUDA"):
		return asr.ProviderCUDA
	default:
		return ""
	}
}

func metalDevice(detail string) (string, string) {
	backend, description, ok := strings.Cut(detail, " (")
	if !ok {
		return strings.TrimSpace(detail), strings.TrimSpace(detail)
	}
	backend = strings.TrimSpace(backend)
	description = strings.TrimSuffix(strings.TrimSpace(description), ")")
	if description == "" {
		description = backend
	}
	return backend, description
}
