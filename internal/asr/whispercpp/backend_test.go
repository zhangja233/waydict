package whispercpp

import (
	"testing"

	"waydict/internal/asr"
)

func TestBackendDetectorVulkan(t *testing.T) {
	var detector backendDetector
	detector.observe("ggml_vulkan: 0 = AMD Radeon RX 5700 (RADV NAVI10) (radv) | uma: 0 | fp16: 1\n")
	detector.observe("whisper_backend_init_gpu: using Vulkan0 backend\n")

	report := detector.backend()
	if report.Provider != asr.ProviderVulkan || report.DeviceName != "AMD Radeon RX 5700 (RADV NAVI10) (radv)" || !report.GPU {
		t.Fatalf("backend = %+v, want detected Vulkan device", report)
	}
}

func TestBackendDetectorMetal(t *testing.T) {
	var detector backendDetector
	detector.observe("ggml_metal_library_init: using embedded metal library\n")
	detector.observe("ggml_metal_device_init: GPU name:   MTL0 (Apple M4)\n")
	detector.observe("whisper_backend_init_gpu: using MTL0 backend\n")
	detector.observe("ggml_metal_init: picking default device: Apple M4\n")

	report := detector.backend()
	if report != (asr.BackendReport{Provider: asr.ProviderMetal, DeviceName: "Apple M4", GPU: true}) {
		t.Fatalf("backend = %+v, want confirmed Metal device", report)
	}
}

func TestBackendDetectorDoesNotAssumeEnumeratedGPUIsActive(t *testing.T) {
	var detector backendDetector
	detector.observe("ggml_vulkan: 0 = Discrete GPU | uma: 0\n")
	detector.observe("whisper_model_load:          CPU total size = 147.37 MB\n")

	report := detector.backend()
	if report.Provider != asr.ProviderCPU || report.DeviceName != "CPU" || report.GPU {
		t.Fatalf("backend = %+v, want CPU", report)
	}
}

func TestBackendDetectorFallsBackToLoggedBackendName(t *testing.T) {
	var detector backendDetector
	detector.observe("whisper_backend_init_gpu: using Vulkan2 backend\n")

	report := detector.backend()
	if report.Provider != asr.ProviderVulkan || report.DeviceName != "Vulkan2" || !report.GPU {
		t.Fatalf("backend = %+v, want logged backend name", report)
	}
}
