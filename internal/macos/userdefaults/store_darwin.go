//go:build darwin && cgo

package userdefaults

/*
#cgo CFLAGS: -fobjc-arc -mmacosx-version-min=13.0
#cgo LDFLAGS: -framework Foundation
#include <stdlib.h>
#include "store.h"
*/
import "C"

import (
	"context"
	"fmt"
	"strings"
	"unsafe"

	"waydict/internal/preferences"
)

type store struct{}

var _ preferences.Store = store{}

func New() preferences.Store {
	return store{}
}

func (store) String(ctx context.Context, key string) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	if strings.IndexByte(key, 0) >= 0 {
		return "", false, fmt.Errorf("NSUserDefaults key contains NUL")
	}
	nativeKey := C.CString(key)
	defer C.free(unsafe.Pointer(nativeKey))
	var value *C.char
	var found C.bool
	result := C.waydict_defaults_copy_string(nativeKey, &value, &found)
	if result != 0 {
		return "", false, fmt.Errorf("read NSUserDefaults key %q: native error %d", key, int(result))
	}
	if !bool(found) {
		return "", false, nil
	}
	defer C.waydict_defaults_free(unsafe.Pointer(value))
	return C.GoString(value), true, ctx.Err()
}

func (store) SetString(ctx context.Context, key, value string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.IndexByte(key, 0) >= 0 || strings.IndexByte(value, 0) >= 0 {
		return fmt.Errorf("NSUserDefaults key or value contains NUL")
	}
	nativeKey := C.CString(key)
	nativeValue := C.CString(value)
	defer C.free(unsafe.Pointer(nativeKey))
	defer C.free(unsafe.Pointer(nativeValue))
	if result := C.waydict_defaults_set_string(nativeKey, nativeValue); result != 0 {
		return fmt.Errorf("write NSUserDefaults key %q: native error %d", key, int(result))
	}
	return ctx.Err()
}

func (store) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.IndexByte(key, 0) >= 0 {
		return fmt.Errorf("NSUserDefaults key contains NUL")
	}
	nativeKey := C.CString(key)
	defer C.free(unsafe.Pointer(nativeKey))
	if result := C.waydict_defaults_delete(nativeKey); result != 0 {
		return fmt.Errorf("delete NSUserDefaults key %q: native error %d", key, int(result))
	}
	return ctx.Err()
}
