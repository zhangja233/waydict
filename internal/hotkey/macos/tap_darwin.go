//go:build darwin && cgo

package macos

/*
#cgo CFLAGS: -fobjc-arc -fblocks -mmacosx-version-min=13.0
#cgo LDFLAGS: -framework ApplicationServices -framework CoreFoundation -framework CoreGraphics
#include "native.h"
*/
import "C"

import (
	"context"
	"fmt"
	"sync"
	"time"

	"waydict/internal/apperr"
	"waydict/internal/hotkey"
)

const Supported = true

type Service struct {
	mu       sync.Mutex
	native   *C.wd_hotkey_service_t
	binding  hotkey.Binding
	handler  hotkey.Handler
	pumpDone chan struct{}
	stopping bool
	stopDone chan struct{}
	cached   hotkey.Status
}

func New() *Service {
	return &Service{}
}

func (s *Service) Available(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	if s.native != nil {
		status := s.nativeStatusLocked()
		s.mu.Unlock()
		if status.Running {
			return nil
		}
		if status.LastErrorCode != "" {
			return statusError(status.LastErrorCode)
		}
	} else {
		s.mu.Unlock()
	}
	var nativeError C.int32_t
	if !bool(C.wd_hotkey_available(&nativeError)) {
		return errorForNative(int32(nativeError), "check global hotkey")
	}
	return ctx.Err()
}

func (s *Service) Start(ctx context.Context, binding hotkey.Binding, handler hotkey.Handler) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := hotkey.ValidateBinding(binding); err != nil {
		return err
	}
	if handler == nil {
		return fmt.Errorf("hotkey handler is nil")
	}
	mode, ok := nativeMode(binding.Mode)
	if !ok {
		return fmt.Errorf("unsupported hotkey mode %q", binding.Mode)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.native != nil || s.stopping {
		return fmt.Errorf("hotkey listener is already started")
	}
	var nativeError C.int32_t
	native := C.wd_hotkey_start(
		C.uint16_t(binding.KeyCode),
		C.uint32_t(binding.Modifiers),
		C.int32_t(mode),
		&nativeError,
	)
	if native == nil {
		err := errorForNative(int32(nativeError), "start global hotkey")
		s.cached = hotkey.Status{Binding: binding, LastErrorCode: apperr.Code(err)}
		return err
	}
	done := make(chan struct{})
	s.native = native
	s.binding = binding
	s.handler = handler
	s.pumpDone = done
	s.cached = hotkey.Status{Running: true, Binding: binding}
	go pump(native, handler, done)
	return nil
}

func (s *Service) Rebind(ctx context.Context, binding hotkey.Binding) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := hotkey.ValidateBinding(binding); err != nil {
		return err
	}
	mode, ok := nativeMode(binding.Mode)
	if !ok {
		return fmt.Errorf("unsupported hotkey mode %q", binding.Mode)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.native == nil || s.stopping {
		return apperr.New(apperr.CodeHotkeyUnavailable, "rebind global hotkey", fmt.Errorf("listener is not running"))
	}
	var nativeError C.int32_t
	if !bool(C.wd_hotkey_rebind(
		s.native,
		C.uint16_t(binding.KeyCode),
		C.uint32_t(binding.Modifiers),
		C.int32_t(mode),
		&nativeError,
	)) {
		return errorForNative(int32(nativeError), "rebind global hotkey")
	}
	s.binding = binding
	s.cached.Binding = binding
	return nil
}

func (s *Service) Stop(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	if s.native == nil {
		s.mu.Unlock()
		return nil
	}
	if s.stopping {
		done := s.stopDone
		s.mu.Unlock()
		select {
		case <-done:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	s.stopping = true
	s.stopDone = make(chan struct{})
	stopDone := s.stopDone
	native := s.native
	pumpDone := s.pumpDone
	s.mu.Unlock()

	C.wd_hotkey_stop(native)
	if pumpDone != nil {
		<-pumpDone
	}
	var nativeStatus C.wd_hotkey_status_t
	C.wd_hotkey_status(native, &nativeStatus)

	s.mu.Lock()
	C.wd_hotkey_destroy(native)
	s.cached.Running = false
	s.cached.DisableCount = int(nativeStatus.disable_count)
	s.cached.LastErrorCode = errorCodeForNative(int32(nativeStatus.last_error))
	s.native = nil
	s.handler = nil
	s.pumpDone = nil
	s.stopping = false
	close(stopDone)
	s.mu.Unlock()
	return nil
}

func (s *Service) Status() hotkey.Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.native != nil {
		return s.nativeStatusLocked()
	}
	return s.cached
}

func (s *Service) nativeStatusLocked() hotkey.Status {
	var nativeStatus C.wd_hotkey_status_t
	C.wd_hotkey_status(s.native, &nativeStatus)
	s.cached.Running = bool(nativeStatus.running)
	s.cached.Binding = s.binding
	s.cached.DisableCount = int(nativeStatus.disable_count)
	s.cached.LastErrorCode = errorCodeForNative(int32(nativeStatus.last_error))
	return s.cached
}

func pump(native *C.wd_hotkey_service_t, handler hotkey.Handler, done chan<- struct{}) {
	defer close(done)
	for {
		var event C.wd_hotkey_event_t
		if C.wd_hotkey_next_event(native, &event) == 0 {
			return
		}
		action, ok := actionFromNative(int32(event.action))
		if !ok {
			continue
		}
		at := time.Now()
		if event.timestamp_ns > 0 {
			at = time.Unix(0, int64(event.timestamp_ns))
		}
		handler(hotkey.Event{Action: action, At: at})
	}
}

func nativeMode(mode hotkey.Mode) (int32, bool) {
	switch mode {
	case hotkey.ModeHold:
		return int32(C.WDHotkeyModeHold), true
	case hotkey.ModeToggle:
		return int32(C.WDHotkeyModeToggle), true
	case hotkey.ModeOneshot:
		return int32(C.WDHotkeyModeOneshot), true
	default:
		return 0, false
	}
}

func actionFromNative(action int32) (hotkey.Action, bool) {
	switch action {
	case int32(C.WDHotkeyEventPress):
		return hotkey.Press, true
	case int32(C.WDHotkeyEventRelease):
		return hotkey.Release, true
	case int32(C.WDHotkeyEventAbort):
		return hotkey.Abort, true
	default:
		return "", false
	}
}

func errorCodeForNative(nativeError int32) string {
	switch nativeError {
	case int32(C.WDHotkeyErrorNone):
		return ""
	case int32(C.WDHotkeyErrorPreflightDenied):
		return apperr.CodePermissionInputMonitoringDenied
	case int32(C.WDHotkeyErrorQueueOverflow):
		return apperr.CodeHotkeyQueueOverflow
	default:
		return apperr.CodeHotkeyUnavailable
	}
}

func errorForNative(nativeError int32, operation string) error {
	code := errorCodeForNative(nativeError)
	if code == "" {
		code = apperr.CodeHotkeyUnavailable
	}
	var context string
	switch nativeError {
	case int32(C.WDHotkeyErrorPreflightDenied):
		context = "Input Monitoring permission is not granted"
	case int32(C.WDHotkeyErrorTapCreate):
		context = "CGEventTapCreate returned NULL after Input Monitoring preflight"
	case int32(C.WDHotkeyErrorRunLoopSource):
		context = "create event-tap run-loop source"
	case int32(C.WDHotkeyErrorThreadCreate):
		context = "create dedicated event-tap thread"
	case int32(C.WDHotkeyErrorTapEnable):
		context = "event tap did not become enabled"
	case int32(C.WDHotkeyErrorNotRunning):
		context = "event tap is not running"
	case int32(C.WDHotkeyErrorQueueOverflow):
		context = "native hotkey transition queue overflowed"
	case int32(C.WDHotkeyErrorRepeatedDisable):
		context = "event tap was disabled three times within 60 seconds"
	case int32(C.WDHotkeyErrorBindingActive):
		context = "configured shortcut is currently pressed"
	default:
		context = fmt.Sprintf("native event-tap error %d", nativeError)
	}
	return apperr.New(code, operation, fmt.Errorf("%s", context))
}

func statusError(code string) error {
	return apperr.New(code, "check global hotkey", fmt.Errorf("listener is unavailable"))
}

var _ hotkey.Service = (*Service)(nil)
