//go:build darwin && cgo

package loginitem

/*
#cgo CFLAGS: -fobjc-arc -mmacosx-version-min=13.0
#cgo LDFLAGS: -framework ServiceManagement -framework Foundation
#include "loginitem.h"
*/
import "C"

import (
	"context"
	"errors"
	"fmt"

	loginitemmodel "waydict/internal/loginitem"
)

const (
	nativeStatusNotRegistered = iota
	nativeStatusEnabled
	nativeStatusRequiresApproval
	nativeStatusNotFound
)

type service struct{}

var _ loginitemmodel.Service = (*service)(nil)

func New() loginitemmodel.Service {
	return &service{}
}

func (s *service) Status(ctx context.Context) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	switch status := int(C.waydict_loginitem_status()); status {
	case nativeStatusNotRegistered, nativeStatusRequiresApproval:
		return false, nil
	case nativeStatusEnabled:
		return true, nil
	case nativeStatusNotFound:
		return false, errors.New("main app login item was not found")
	default:
		return false, fmt.Errorf("unknown main app login item status %d", status)
	}
}

func (s *service) SetEnabled(ctx context.Context, enabled bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var nativeEnabled C.int
	if enabled {
		nativeEnabled = 1
	}
	var message *C.char
	result := C.waydict_loginitem_set_enabled(nativeEnabled, &message)
	if message != nil {
		defer C.waydict_loginitem_free_error(message)
	}
	if result != 0 {
		return nil
	}
	operation := "unregister"
	if enabled {
		operation = "register"
	}
	if message == nil {
		return fmt.Errorf("%s main app login item: native operation failed", operation)
	}
	return fmt.Errorf("%s main app login item: %s", operation, C.GoString(message))
}
