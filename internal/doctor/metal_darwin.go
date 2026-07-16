//go:build darwin && cgo

package doctor

/*
#cgo CFLAGS: -fobjc-arc -mmacosx-version-min=13.0
#cgo LDFLAGS: -framework Metal -framework Foundation
#include "metal.h"
*/
import "C"

import (
	"fmt"
	"unsafe"
)

func metalPreflight() (string, error) {
	var name [512]C.char
	if C.waydict_doctor_metal_device(&name[0], C.size_t(len(name))) == 0 {
		return "", fmt.Errorf("no default Metal device")
	}
	return C.GoString((*C.char)(unsafe.Pointer(&name[0]))), nil
}
