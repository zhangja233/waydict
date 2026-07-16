//go:build !darwin && !linux

package modelinstall

import "errors"

func exchangePaths(string, string) error {
	return errors.New("atomic directory exchange is unavailable")
}
