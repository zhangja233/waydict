//go:build coreaudio && cgo && darwin

package coreaudio

/*
#include "native.h"
*/
import "C"

import (
	"context"
	"fmt"
	"unsafe"

	"waydict/internal/apperr"
	"waydict/internal/audio"
)

type manager struct{}

var _ audio.DeviceManager = manager{}

func Manager() audio.DeviceManager {
	return manager{}
}

func (manager) Devices(ctx context.Context) ([]audio.Device, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var list *C.wd_ca_device_list
	var detail *C.char
	status := C.wd_ca_copy_devices(&list, &detail)
	if status != C.WD_CA_STATUS_OK {
		return nil, nativeError(status, "enumerate CoreAudio inputs", consumeDetail(detail))
	}
	defer C.wd_ca_device_list_destroy(list)
	items := unsafe.Slice(list.items, int(list.count))
	devices := make([]audio.Device, 0, len(items))
	for _, item := range items {
		devices = append(devices, audio.Device{
			ID:        C.GoString(item.uid),
			Name:      C.GoString(item.name),
			Default:   bool(item.is_default),
			Connected: bool(item.connected),
		})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return devices, nil
}

func Check() error {
	devices, err := (manager{}).Devices(context.Background())
	if err != nil {
		return err
	}
	if len(devices) == 0 {
		return apperr.New(apperr.CodeAudioBackendUnavailable, "check CoreAudio", fmt.Errorf("no CoreAudio devices with input streams were found"))
	}
	var permission C.wd_ca_permission
	var detail *C.char
	status := C.wd_ca_permission_state(&permission, &detail)
	if status != C.WD_CA_STATUS_OK {
		return nativeError(status, "check CoreAudio microphone permission", consumeDetail(detail))
	}
	if permission != C.WD_CA_PERMISSION_GRANTED {
		state := "unknown"
		switch permission {
		case C.WD_CA_PERMISSION_NOT_DETERMINED:
			state = "not_determined"
		case C.WD_CA_PERMISSION_RESTRICTED:
			state = "restricted"
		case C.WD_CA_PERMISSION_DENIED:
			state = "denied"
		}
		return &apperr.Error{
			Code:      apperr.CodePermissionMicrophoneDenied,
			Operation: "check CoreAudio microphone permission",
			Err:       fmt.Errorf("permission state is %s", state),
		}
	}
	return nil
}
