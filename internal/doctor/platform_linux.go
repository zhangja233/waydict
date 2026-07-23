//go:build linux

package doctor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"waydict/internal/audio/pipewire"
	"waydict/internal/buildinfo"
	"waydict/internal/config"
	"waydict/internal/inject"
	"waydict/internal/swayipc"
)

type linuxRegistry struct{}

func Current() Registry { return linuxRegistry{} }

func (linuxRegistry) Checks(ctx context.Context, cfg config.Config) []Result {
	results := []Result{vulkanResult(), cudaResult()}
	for _, name := range []string{"WAYLAND_DISPLAY", "SWAYSOCK", "XDG_RUNTIME_DIR"} {
		var err error
		if os.Getenv(name) == "" {
			err = fmt.Errorf("%s is not set", name)
		}
		results = append(results, errorResult(name, err))
	}
	var sherpaErr error
	if !buildinfo.SherpaEnabled {
		sherpaErr = fmt.Errorf("rebuild with -tags sherpa and CGO_ENABLED=1")
	}
	results = append(results, errorResult("sherpa build", sherpaErr))
	var buildErr error
	if !buildinfo.PipeWireEnabled {
		buildErr = fmt.Errorf("rebuild with -tags pipewire and libpipewire-0.3 development files")
	}
	results = append(results,
		errorResult("PipeWire build", buildErr),
		errorResult("wtype", inject.NewWtype(cfg.Injection).Available(ctx)),
		errorResult("PipeWire", pipewire.Check()),
	)
	focus := swayipc.New(cfg.Sway.Socket)
	fctx, cancel := context.WithTimeout(ctx, time.Second)
	results = append(results, errorResult("Sway IPC", focus.Available(fctx)))
	cancel()
	return append(results, remoteASRResults(ctx, cfg)...)
}

func vulkanResult() Result {
	for _, dir := range []string{"/run/opengl-driver/share/vulkan/icd.d", "/usr/share/vulkan/icd.d"} {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return Result{Level: Info, Name: "Vulkan ICD", Detail: "found " + filepath.Clean(dir)}
		}
	}
	return Result{Level: Info, Name: "Vulkan ICD", Detail: "no ICD directory found; install a Vulkan driver if GPU ASR is desired"}
}

// cudaResult reports the kernel side only. A loaded driver does not imply the
// binary was linked against a CUDA-enabled libwhisper — `waydict version` shows
// which backends the build actually carries.
func cudaResult() Result {
	if _, err := os.Stat("/dev/nvidiactl"); err != nil {
		return Result{Level: Info, Name: "CUDA driver", Detail: "no /dev/nvidiactl; nvidia kernel modules not loaded"}
	}
	nodes, _ := filepath.Glob("/dev/nvidia[0-9]*")
	if len(nodes) == 0 {
		return Result{Level: Info, Name: "CUDA driver", Detail: "/dev/nvidiactl present but no GPU device node"}
	}
	return Result{Level: Info, Name: "CUDA driver", Detail: fmt.Sprintf("found %d nvidia device node(s)", len(nodes))}
}
