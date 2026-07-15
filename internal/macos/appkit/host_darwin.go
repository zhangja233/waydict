//go:build darwin && cgo

package appkit

/*
#cgo CFLAGS: -fobjc-arc -fblocks -mmacosx-version-min=13.0
#cgo LDFLAGS: -framework AppKit -framework Foundation -framework CoreGraphics
#include <stdlib.h>
#include "host.h"
#include "../native/waydict_events.h"
*/
import "C"

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"sync/atomic"
	"unicode/utf8"
	"unsafe"
)

const (
	MaxActionPayloadBytes = 4 * 1024
	actionQueueSize       = 64
)

type Action int32

const (
	ActionStartHold Action = iota + 1
	ActionToggle
	ActionStartOneshot
	ActionStopCommit
	ActionStopDiscard
	ActionReloadConfig
	ActionInstallRequiredModels
	ActionRevealModels
	ActionSelectAudioDevice
	ActionSetHotkeyMode
	ActionRequestMicrophonePermission
	ActionRequestAccessibilityPermission
	ActionRequestInputMonitoringPermission
	ActionSetLaunchAtLogin
	ActionOpenConfig
	ActionRestartRuntime
	ActionOpenLog
	ActionRunDiagnostics
	ActionCopyDiagnostics
	ActionQuit
	ActionSystemWillSleep
	ActionSystemDidWake
)

type Event struct {
	Action  Action
	Payload string
	Number  int64
}

type Installation struct {
	BundlePath   string `json:"bundle_path"`
	Translocated bool   `json:"translocated"`
	ReadOnly     bool   `json:"read_only"`
	Blocked      bool   `json:"blocked"`
}

type AudioDevice struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Default   bool   `json:"default,omitempty"`
	Connected bool   `json:"connected"`
}

type ViewModel struct {
	State                 string        `json:"state"`
	LastError             string        `json:"last_error,omitempty"`
	LastWarning           string        `json:"last_warning,omitempty"`
	MicrophonePermission  string        `json:"microphone_permission,omitempty"`
	Accessibility         string        `json:"accessibility_permission,omitempty"`
	InputMonitoring       string        `json:"input_monitoring_permission,omitempty"`
	LaunchAtLogin         bool          `json:"launch_at_login"`
	LaunchAtLoginError    string        `json:"launch_at_login_error,omitempty"`
	HotkeyMode            string        `json:"hotkey_mode,omitempty"`
	HotkeyDescription     string        `json:"hotkey_description,omitempty"`
	HotkeyAvailable       bool          `json:"hotkey_available"`
	AudioDeviceName       string        `json:"audio_device_name,omitempty"`
	AudioDeviceID         string        `json:"audio_device_id,omitempty"`
	SelectedAudioDevice   string        `json:"selected_audio_device_uid,omitempty"`
	AudioDeviceControlled bool          `json:"audio_device_controlled"`
	AudioDevices          []AudioDevice `json:"audio_devices,omitempty"`
	ASREngine             string        `json:"asr_engine,omitempty"`
	ASRModel              string        `json:"asr_model,omitempty"`
	ASRProvider           string        `json:"asr_provider,omitempty"`
	ModelsReady           bool          `json:"models_ready"`
	ModelStatus           string        `json:"model_status,omitempty"`
	PendingRestart        bool          `json:"pending_restart"`
	InstallingModels      bool          `json:"installing_models"`
	InstallationBlocked   bool          `json:"installation_blocked"`
	InstallationMessage   string        `json:"installation_message,omitempty"`
	Version               string        `json:"version,omitempty"`
	Commit                string        `json:"commit,omitempty"`
	BuildNumber           string        `json:"build_number,omitempty"`
	BuildTags             string        `json:"build_tags,omitempty"`
	Architecture          string        `json:"architecture,omitempty"`
	Platform              string        `json:"platform,omitempty"`
	ConfigPath            string        `json:"config_path,omitempty"`
	LegacyConfig          bool          `json:"legacy_config"`
	MigrationWarning      string        `json:"migration_warning,omitempty"`
	AudioBackend          string        `json:"audio_backend,omitempty"`
	InjectionBackend      string        `json:"injection_backend,omitempty"`
	FocusBackend          string        `json:"focus_backend,omitempty"`
	SocketPath            string        `json:"socket_path,omitempty"`
}

type eventSink struct {
	queue chan Event
}

var activeSink atomic.Pointer[eventSink]

type Host struct {
	native       C.waydict_host_t
	sink         *eventSink
	installation Installation
	destroyed    atomic.Bool
}

func New() (*Host, error) {
	if runtime.GOOS != "darwin" {
		return nil, errors.New("AppKit host requires macOS")
	}
	native := C.waydict_host_create()
	if native == nil {
		return nil, errors.New("create AppKit host")
	}
	host := &Host{native: native, sink: &eventSink{queue: make(chan Event, actionQueueSize)}}
	if !activeSink.CompareAndSwap(nil, host.sink) {
		C.waydict_host_destroy(native)
		return nil, errors.New("an AppKit host already exists")
	}
	value := C.waydict_host_copy_installation_json(native)
	if value != nil {
		if err := json.Unmarshal([]byte(C.GoString(value)), &host.installation); err != nil {
			activeSink.CompareAndSwap(host.sink, nil)
			C.waydict_host_free_string(value)
			C.waydict_host_destroy(native)
			return nil, fmt.Errorf("decode installation guard: %w", err)
		}
		C.waydict_host_free_string(value)
	}
	return host, nil
}

func (h *Host) Installation() Installation {
	if h == nil {
		return Installation{}
	}
	return h.installation
}

func (h *Host) Events() <-chan Event {
	if h == nil || h.sink == nil {
		return nil
	}
	return h.sink.queue
}

func (h *Host) Run() error {
	if h == nil || h.native == nil {
		return errors.New("AppKit host is unavailable")
	}
	if result := C.waydict_host_run(h.native); result != 0 {
		return fmt.Errorf("AppKit run loop failed: %d", int(result))
	}
	return nil
}

func (h *Host) Terminate() {
	if h != nil && h.native != nil && !h.destroyed.Load() {
		C.waydict_host_terminate(h.native)
	}
}

func (h *Host) Destroy() {
	if h == nil || h.native == nil || !h.destroyed.CompareAndSwap(false, true) {
		return
	}
	activeSink.CompareAndSwap(h.sink, nil)
	C.waydict_host_destroy(h.native)
	h.native = nil
}

func (h *Host) Update(view ViewModel) error {
	if h == nil || h.native == nil || h.destroyed.Load() {
		return errors.New("AppKit host is unavailable")
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		return err
	}
	var pointer *C.char
	if len(encoded) != 0 {
		pointer = (*C.char)(unsafe.Pointer(&encoded[0]))
	}
	C.waydict_host_update_status(h.native, pointer, C.size_t(len(encoded)))
	runtime.KeepAlive(encoded)
	return nil
}

func (h *Host) ShowError(code, message string) {
	if h == nil || h.native == nil || h.destroyed.Load() {
		return
	}
	nativeCode := C.CString(code)
	nativeMessage := C.CString(message)
	defer C.free(unsafe.Pointer(nativeCode))
	defer C.free(unsafe.Pointer(nativeMessage))
	C.waydict_host_show_error(h.native, nativeCode, nativeMessage)
}

func (h *Host) ShowDiagnostics(copyOnly bool) {
	if h == nil || h.native == nil || h.destroyed.Load() {
		return
	}
	C.waydict_host_show_diagnostics(h.native, C.bool(copyOnly))
}

func (h *Host) OpenPath(ctx context.Context, path string) error {
	return h.pathOperation(ctx, path, false)
}

func (h *Host) RevealPath(ctx context.Context, path string) error {
	return h.pathOperation(ctx, path, true)
}

func (h *Host) pathOperation(ctx context.Context, path string, reveal bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if h == nil || h.native == nil || h.destroyed.Load() {
		return errors.New("AppKit host is unavailable")
	}
	nativePath := C.CString(path)
	defer C.free(unsafe.Pointer(nativePath))
	var ok C.bool
	if reveal {
		ok = C.waydict_host_reveal_path(h.native, nativePath)
	} else {
		ok = C.waydict_host_open_path(h.native, nativePath)
	}
	if !bool(ok) {
		return fmt.Errorf("open %q with Finder", path)
	}
	return ctx.Err()
}

//export waydictAppkitEvent
func waydictAppkitEvent(action C.int32_t, payload *C.char, payloadLength C.size_t, number C.int64_t) C.bool {
	sink := activeSink.Load()
	if sink == nil || uint64(payloadLength) > MaxActionPayloadBytes {
		return C.bool(false)
	}
	var value string
	if payloadLength != 0 {
		if payload == nil {
			return C.bool(false)
		}
		value = string(C.GoBytes(unsafe.Pointer(payload), C.int(payloadLength)))
		if !utf8.ValidString(value) {
			return C.bool(false)
		}
	}
	event := Event{Action: Action(action), Payload: value, Number: int64(number)}
	select {
	case sink.queue <- event:
		return C.bool(true)
	default:
		return C.bool(false)
	}
}
