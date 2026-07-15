//go:build darwin && cgo

package appkit

/*
#include <stdlib.h>
#include "host.h"
*/
import "C"

import (
	"context"
	"fmt"
	"time"
	"unsafe"

	"waydict/internal/apphost"
)

type activator struct{}

var _ apphost.Activator = (*activator)(nil)

func NewActivator() apphost.Activator {
	return &activator{}
}

func (*activator) ActivateBundle(ctx context.Context, bundleID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	nativeID := C.CString(bundleID)
	defer C.free(unsafe.Pointer(nativeID))
	if !bool(C.waydict_activate_bundle(nativeID)) {
		return fmt.Errorf("activate bundle %q", bundleID)
	}
	return ctx.Err()
}

func (*activator) ActivatePID(ctx context.Context, pid int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !bool(C.waydict_activate_pid(C.int32_t(pid))) {
		return fmt.Errorf("activate pid %d", pid)
	}
	return ctx.Err()
}

func (*activator) WaitFrontmostPID(ctx context.Context, pid int) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if int(C.waydict_frontmost_pid()) == pid {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
