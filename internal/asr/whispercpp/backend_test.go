package whispercpp

import "testing"

func TestBackendDetectorVulkan(t *testing.T) {
	var detector backendDetector
	detector.observe("ggml_vulkan: 0 = AMD Radeon RX 5700 (RADV NAVI10) (radv) | uma: 0 | fp16: 1\n")
	detector.observe("whisper_backend_init_gpu: using Vulkan0 backend\n")

	name, gpu := detector.backend()
	if name != "AMD Radeon RX 5700 (RADV NAVI10) (radv)" || !gpu {
		t.Fatalf("backend = (%q, %t), want detected Vulkan device", name, gpu)
	}
}

func TestBackendDetectorDoesNotAssumeEnumeratedGPUIsActive(t *testing.T) {
	var detector backendDetector
	detector.observe("ggml_vulkan: 0 = Discrete GPU | uma: 0\n")
	detector.observe("whisper_model_load:          CPU total size = 147.37 MB\n")

	name, gpu := detector.backend()
	if name != "CPU" || gpu {
		t.Fatalf("backend = (%q, %t), want CPU", name, gpu)
	}
}

func TestBackendDetectorFallsBackToLoggedBackendName(t *testing.T) {
	var detector backendDetector
	detector.observe("whisper_backend_init_gpu: using Vulkan2 backend\n")

	name, gpu := detector.backend()
	if name != "Vulkan2" || !gpu {
		t.Fatalf("backend = (%q, %t), want logged backend name", name, gpu)
	}
}
