//go:build darwin

package main

import (
	"context"
	"os/exec"
)

func launchWaydictBundle(ctx context.Context, bundleID string) error {
	return exec.CommandContext(ctx, "/usr/bin/open", "-gj", "-b", bundleID).Run()
}
