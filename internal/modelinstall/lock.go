package modelinstall

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"waydict/internal/apperr"
	"waydict/internal/config"
)

const installLockName = "model-install.lock"

type InstallLock struct {
	file *os.File
}

func Acquire(ctx context.Context, opts InstallOptions) (*InstallLock, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	stateDir := opts.StateDir
	if stateDir == "" {
		stateDir = config.CurrentPlatformPaths().StateDir
	}
	if err := preparePrivateDir(stateDir); err != nil {
		return nil, fmt.Errorf("prepare model install state directory: %w", err)
	}
	file, err := openInstallLock(filepath.Join(stateDir, installLockName))
	if err != nil {
		if errors.Is(err, errInstallLockBusy) {
			return nil, apperr.New(apperr.CodeModelInstallBusy, "install models", errors.New("another Waydict process is installing models"))
		}
		return nil, fmt.Errorf("acquire model install lock: %w", err)
	}
	lock := &InstallLock{file: file}
	now := time.Now()
	if err := cleanupStalePartials(cacheDownloadsDir(opts), now); err != nil {
		_ = lock.Close()
		return nil, err
	}
	modelsDir := opts.Dir
	if modelsDir == "" {
		modelsDir = config.CurrentPlatformPaths().ModelsDir
	}
	for _, dir := range []string{modelsDir, filepath.Join(modelsDir, "whisper")} {
		if err := cleanupStalePartials(dir, now); err != nil {
			_ = lock.Close()
			return nil, err
		}
	}
	return lock, nil
}

func (l *InstallLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := closeInstallLock(l.file)
	l.file = nil
	return err
}

func (l *InstallLock) options(opts InstallOptions) InstallOptions {
	opts.lock = l
	return opts
}

func (l *InstallLock) LockedOptions(opts InstallOptions) InstallOptions {
	return l.options(opts)
}

func WithLock(ctx context.Context, opts InstallOptions, fn func(InstallOptions) error) error {
	if fn == nil {
		return errors.New("model install transaction is nil")
	}
	if opts.lock != nil {
		return fn(opts)
	}
	lock, err := Acquire(ctx, opts)
	if err != nil {
		return err
	}
	defer lock.Close()
	return fn(lock.options(opts))
}

func withLockResult(ctx context.Context, opts InstallOptions, fn func(InstallOptions) (string, error)) (string, error) {
	if opts.lock != nil {
		return fn(opts)
	}
	lock, err := Acquire(ctx, opts)
	if err != nil {
		return "", err
	}
	defer lock.Close()
	return fn(lock.options(opts))
}

func cacheDownloadsDir(opts InstallOptions) string {
	root := opts.CacheDir
	if root == "" {
		root = config.CurrentPlatformPaths().CacheDir
	}
	return filepath.Join(root, "downloads")
}

func cleanupStalePartials(dir string, now time.Time) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect model download cache: %w", err)
	}
	cutoff := now.Add(-24 * time.Hour)
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".partial") || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		info, err := entry.Info()
		if err != nil || (!info.Mode().IsRegular() && !info.IsDir()) || !info.ModTime().Before(cutoff) || !ownedByCurrentUser(info) {
			continue
		}
		if err := os.RemoveAll(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove stale model download %s: %w", entry.Name(), err)
		}
	}
	return nil
}

func ownedByCurrentUser(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return !ok || int(stat.Uid) == os.Geteuid()
}

func preparePrivateDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || !ownedByCurrentUser(info) {
		return fmt.Errorf("%s is not a current-user directory", path)
	}
	if err := os.Chmod(path, 0700); err != nil {
		return err
	}
	return nil
}
