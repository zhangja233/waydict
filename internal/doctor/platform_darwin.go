//go:build darwin

package doctor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"waydict/internal/buildinfo"
	"waydict/internal/config"
	"waydict/internal/control"
)

type darwinRegistry struct{}

func Current() Registry { return darwinRegistry{} }

func (darwinRegistry) Checks(_ context.Context, cfg config.Config) []Result {
	configDetail := cfg.ActivePath()
	if configDetail == "" {
		configDetail = "defaults (no config file active)"
	}
	results := []Result{
		{Level: Info, Name: "config path", Detail: configDetail},
		{Level: Info, Name: "backends", Detail: fmt.Sprintf("audio=%s injection=%s focus=%s", cfg.Audio.Backend, cfg.Injection.Engine, cfg.Focus.Backend)},
		errorResult("socket path", control.ValidateSocketPathFor("darwin", cfg.Daemon.Socket)),
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
			results = append(results, Result{Level: Info, Name: item.name, Detail: item.path + " (not created yet)"})
		} else {
			results = append(results, errorResult(item.name, err))
		}
	}
	results = append(results, Result{Level: Info, Name: "native checks", Detail: "app-host permission and signing probes are available after PR3 wiring"})
	return results
}
