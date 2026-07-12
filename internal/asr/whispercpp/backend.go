package whispercpp

import "strings"

type backendDetector struct {
	devices map[string]string
	name    string
	gpu     bool
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

	const usingPrefix = "whisper_backend_init_gpu: using "
	if rest, ok := strings.CutPrefix(line, usingPrefix); ok {
		if backend, ok := strings.CutSuffix(rest, " backend"); ok && backend != "" {
			d.name = backend
			if name := d.devices[backend]; name != "" {
				d.name = name
			}
			d.gpu = true
		}
		return
	}

	if strings.Contains(line, "whisper_backend_init_gpu: no GPU found") ||
		(strings.Contains(line, "whisper_model_load:") && strings.Contains(line, "CPU total size")) {
		d.name = "CPU"
		d.gpu = false
	}
}

func (d *backendDetector) backend() (string, bool) {
	return d.name, d.gpu
}
