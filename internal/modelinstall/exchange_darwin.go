//go:build darwin

package modelinstall

import "golang.org/x/sys/unix"

func exchangePaths(staging, final string) error {
	return unix.RenameatxNp(unix.AT_FDCWD, staging, unix.AT_FDCWD, final, unix.RENAME_SWAP)
}
