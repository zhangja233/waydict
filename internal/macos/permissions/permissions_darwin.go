//go:build darwin && cgo

package permissions

/*
#cgo CFLAGS: -fobjc-arc -mmacosx-version-min=13.0
#cgo LDFLAGS: -framework AVFoundation -framework ApplicationServices -framework CoreGraphics -framework AppKit -framework Foundation
#include "permissions.h"
*/
import "C"

import (
	"context"
	"fmt"
	"time"

	permissionmodel "waydict/internal/permissions"
)

type source struct{}

var _ permissionmodel.Source = (*source)(nil)

func New() permissionmodel.Source {
	return &source{}
}

func (s *source) Snapshot(ctx context.Context) (permissionmodel.Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return permissionmodel.UnavailableSnapshot(time.Now()), err
	}
	var native C.waydict_permission_snapshot_t
	C.waydict_permissions_snapshot(&native)
	return permissionmodel.Snapshot{
		Microphone:      stateFromNative(int(native.microphone)),
		Accessibility:   stateFromNative(int(native.accessibility)),
		InputMonitoring: stateFromNative(int(native.input_monitoring)),
		CheckedAt:       time.Now(),
	}, nil
}

func (s *source) Request(ctx context.Context, kind permissionmodel.Kind) (permissionmodel.State, error) {
	if err := ctx.Err(); err != nil {
		return permissionmodel.Unavailable, err
	}
	nativeKind, ok := kindToNative(kind)
	if !ok {
		return permissionmodel.Unavailable, fmt.Errorf("unsupported permission kind %q", kind)
	}
	var nativeState C.int
	result := C.waydict_permissions_request(C.int(nativeKind), &nativeState)
	state := stateFromNative(int(nativeState))
	if err := permissionResultError(int(result), "request", kind); err != nil {
		return state, err
	}
	if err := ctx.Err(); err != nil {
		return state, err
	}
	return state, nil
}

func (s *source) OpenSettings(ctx context.Context, kind permissionmodel.Kind) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	nativeKind, ok := kindToNative(kind)
	if !ok {
		return fmt.Errorf("unsupported permission kind %q", kind)
	}
	result := C.waydict_permissions_open_settings(C.int(nativeKind))
	return permissionResultError(int(result), "open settings for", kind)
}

func permissionResultError(result int, operation string, kind permissionmodel.Kind) error {
	switch result {
	case nativeResultOK:
		return nil
	case nativeResultInvalidKind:
		return fmt.Errorf("%s unsupported permission kind %q", operation, kind)
	case nativeResultOpenSettingsFailed:
		return fmt.Errorf("%s %s permission: System Settings did not open", operation, kind)
	default:
		return fmt.Errorf("%s %s permission: native error %d", operation, kind, result)
	}
}
