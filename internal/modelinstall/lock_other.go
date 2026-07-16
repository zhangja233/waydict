//go:build !darwin && !linux

package modelinstall

import (
	"errors"
	"os"
)

var errInstallLockBusy = errors.New("model install lock is busy")

func openInstallLock(string) (*os.File, error) {
	return nil, errors.New("advisory model install locking is unavailable")
}

func closeInstallLock(*os.File) error { return nil }
