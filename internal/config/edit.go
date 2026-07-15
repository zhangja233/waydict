package config

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed sample-config-macos.toml
var macOSSampleConfig []byte

type FilePresenter interface {
	Open(context.Context, string) error
	Reveal(context.Context, string) error
}

func SampleConfigFor(platform string) []byte {
	if platform != "darwin" {
		return nil
	}
	return append([]byte(nil), macOSSampleConfig...)
}

func EnsureConfigForEditing(activePath string, paths PlatformPaths, sample []byte) (path string, created bool, err error) {
	if activePath != "" {
		return activePath, false, nil
	}
	path = paths.ConfigFile
	if path == "" {
		return "", false, fmt.Errorf("config path is empty")
	}
	if _, err := os.Lstat(path); err == nil {
		return path, false, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", false, err
	}
	dir := filepath.Dir(path)
	if err := ensurePrivateConfigDir(dir); err != nil {
		return "", false, err
	}
	tmp, err := os.CreateTemp(dir, ".config.toml.tmp-")
	if err != nil {
		return "", false, err
	}
	tmpPath := tmp.Name()
	keep := false
	defer func() {
		_ = tmp.Close()
		if !keep {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0600); err != nil {
		return "", false, err
	}
	if _, err := tmp.Write(sample); err != nil {
		return "", false, err
	}
	if err := tmp.Sync(); err != nil {
		return "", false, err
	}
	if err := tmp.Close(); err != nil {
		return "", false, err
	}
	if _, err := os.Lstat(path); err == nil {
		return path, false, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", false, err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return "", false, err
	}
	keep = true
	if dirHandle, err := os.Open(dir); err == nil {
		_ = dirHandle.Sync()
		_ = dirHandle.Close()
	}
	return path, true, nil
}

func OpenConfigForEditing(ctx context.Context, activePath string, paths PlatformPaths, presenter FilePresenter) (string, bool, error) {
	path, created, err := EnsureConfigForEditing(activePath, paths, SampleConfigFor("darwin"))
	if err != nil || presenter == nil {
		return path, created, err
	}
	if err := presenter.Open(ctx, path); err == nil {
		return path, created, nil
	}
	if err := presenter.Reveal(ctx, path); err != nil {
		return path, created, err
	}
	return path, created, nil
}

func ensurePrivateConfigDir(dir string) error {
	if info, err := os.Lstat(dir); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("config directory %s is not a directory", dir)
		}
	} else if errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
	} else {
		return err
	}
	return os.Chmod(dir, 0700)
}
