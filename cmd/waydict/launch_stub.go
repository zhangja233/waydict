//go:build !darwin

package main

import (
	"context"
	"fmt"
)

func launchWaydictBundle(context.Context, string) error {
	return fmt.Errorf("Launch Services is unavailable")
}
