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

// Line shapes are verbatim from whisper.cpp built with GGML_CUDA: a header line,
// then one indented Device line per GPU. Unlike Vulkan's single line, the device
// name is comma-terminated rather than pipe-terminated.
func TestBackendDetectorCUDA(t *testing.T) {
	var detector backendDetector
	detector.observe("ggml_cuda_init: found 1 CUDA devices (Total VRAM: 16384 MiB):\n")
	detector.observe("  Device 0: NVIDIA GeForce GPU, compute capability 12.0, VMM: yes, VRAM: 16384 MiB\n")
	detector.observe("whisper_backend_init_gpu: using CUDA0 backend\n")

	report := detector.backend()
	if report.Provider != asr.ProviderCUDA || report.DeviceName != "NVIDIA GeForce GPU" || !report.GPU {
		t.Fatalf("backend = %+v, want detected CUDA device", report)
	}
}

// A bare "Device 0: ..." line without the ggml_cuda_init header must not register:
// other backends print similar lines and would otherwise be claimed as CUDA.
func TestBackendDetectorCUDADeviceLineNeedsHeader(t *testing.T) {
	var detector backendDetector
	detector.observe("  Device 0: Some Other Accelerator, revision 3\n")
	detector.observe("whisper_backend_init_gpu: using CUDA0 backend\n")

	if report := detector.backend(); report.DeviceName != "CUDA0" {
		t.Fatalf("DeviceName = %q, want the raw backend name with no device claimed", report.DeviceName)
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
