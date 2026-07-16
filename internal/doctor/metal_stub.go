//go:build !darwin || !cgo

package doctor

import "fmt"

func metalPreflight() (string, error) {
	return "", fmt.Errorf("Metal preflight requires Darwin with cgo")
}
