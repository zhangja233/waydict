//go:build darwin

package doctor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"

	"waydict/internal/audio/coreaudio"
	"waydict/internal/buildinfo"
	"waydict/internal/config"
	"waydict/internal/control"
)

type darwinRegistry struct{}

func Current() Registry { return darwinRegistry{} }

func (darwinRegistry) Checks(ctx context.Context, cfg config.Config) []Result {
	configDetail := cfg.ActivePath()
	if configDetail == "" {
		configDetail = "defaults (no config file active)"
	}
	results := []Result{
		{Level: Info, Name: "config path", Detail: redactHome(configDetail)},
		{Level: Info, Name: "backends", Detail: fmt.Sprintf("audio=%s injection=%s focus=%s", cfg.Audio.Backend, cfg.Injection.Engine, cfg.Focus.Backend)},
		errorResult("socket path", control.ValidateSocketPathFor("darwin", cfg.Daemon.Socket)),
		buildResult("CoreAudio build", buildinfo.CoreAudioEnabled, "coreaudio,cgo"),
		buildResult("macOS native", buildinfo.MacOSNativeEnabled, "cgo"),
		buildResult("Whisper build", buildinfo.WhisperEnabled, "whispercpp,cgo"),
	}
	for _, warning := range cfg.MigrationWarnings() {
		results = append(results, Result{Level: Warn, Name: "config migration", Detail: redactHome(warning)})
	}
	if detail, err := metalPreflight(); err != nil {
		results = append(results, errorResult("Metal preflight", err))
	} else {
		results = append(results, Result{Level: OK, Name: "Metal preflight", Detail: detail})
	}
	var sherpaErr error
	if !buildinfo.SherpaEnabled {
		sherpaErr = fmt.Errorf("rebuild with -tags sherpa and CGO_ENABLED=1")
	}
	results = append(results, errorResult("sherpa build", sherpaErr))
	paths := cfg.Paths()
	logDir := ""
	if paths.LogFile != "" {
		logDir = filepath.Dir(paths.LogFile)
	}
	for _, item := range []struct {
		name string
		path string
	}{{"model directory", paths.ModelsDir}, {"log directory", logDir}} {
		if item.path == "" {
			continue
		}
		_, err := os.Stat(item.path)
		if os.IsNotExist(err) {
			results = append(results, Result{Level: Info, Name: item.name, Detail: redactHome(item.path) + " (not created yet)"})
		} else {
			if err == nil {
				err = unix.Access(item.path, unix.W_OK)
			}
			results = append(results, errorResult(item.name, err))
		}
	}
	if info, err := os.Lstat(filepath.Dir(cfg.Daemon.Socket)); err == nil {
		owner := -1
		if stat, ok := info.Sys().(*syscall.Stat_t); ok {
			owner = int(stat.Uid)
		}
		results = append(results, Result{Level: Info, Name: "socket directory", Detail: fmt.Sprintf("%s owner_uid=%d mode=%04o", redactHome(filepath.Dir(cfg.Daemon.Socket)), owner, info.Mode().Perm())})
	}
	if buildinfo.CoreAudioEnabled {
		devices, err := coreaudio.Manager().Devices(ctx)
		if err != nil {
			results = append(results, errorResult("CoreAudio devices", err))
		} else {
			selectedID := cfg.Audio.Device
			selectedName := ""
			if selectedID == "" || selectedID == "default" {
				selectedID = "default"
				for _, device := range devices {
					if device.Default {
						selectedName = device.Name
						break
					}
				}
			} else {
				for _, device := range devices {
					if device.ID == selectedID {
						selectedName = device.Name
						break
					}
				}
			}
			results = append(results, Result{Level: Info, Name: "CoreAudio devices", Detail: fmt.Sprintf("inputs=%d selected_uid=%s selected_name=%s", len(devices), selectedID, selectedName)})
		}
		results = append(results, errorResult("CoreAudio", coreaudio.Check()))
	}
	return results
}

func buildResult(name string, enabled bool, tags string) Result {
	if enabled {
		return Result{Level: OK, Name: name}
	}
	return Result{Level: Fail, Name: name, Err: fmt.Errorf("rebuild with -tags %s", tags)}
}

func redactHome(value string) string {
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		return strings.ReplaceAll(value, home, "~")
	}
	return value
}
