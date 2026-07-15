//go:build darwin && cgo

package macos

/*
#cgo CFLAGS: -fobjc-arc -mmacosx-version-min=13.0
#cgo LDFLAGS: -framework ApplicationServices -framework AppKit -framework Foundation
#include "native.h"
*/
import "C"

import (
	"context"
	"fmt"
	"sync"
	"time"
	"unsafe"

	"waydict/internal/apperr"
	"waydict/internal/focus"
)

const retryDelay = 25 * time.Millisecond

type Provider struct {
	mu      sync.Mutex
	native  *C.wd_focus_provider
	initErr error
}

func New() *Provider {
	provider := &Provider{}
	var message *C.char
	result := C.wd_focus_provider_create(&provider.native, &message)
	if result != C.WD_FOCUS_RESULT_OK {
		provider.initErr = nativeError(result, "initialize Accessibility focus", takeMessage(message))
	} else {
		C.wd_focus_free(unsafe.Pointer(message))
	}
	return provider
}

func (p *Provider) Backend() string { return "accessibility" }

func (p *Provider) Available(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if p == nil {
		return focusUnavailable("check Accessibility focus", "focus provider is unavailable")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.initErr != nil {
		return p.initErr
	}
	if p.native == nil {
		return focusUnavailable("check Accessibility focus", "focus provider is closed")
	}
	if C.wd_focus_available() == 0 {
		return apperr.New(apperr.CodePermissionAccessibilityDenied, "check Accessibility focus", fmt.Errorf("Accessibility permission is not granted"))
	}
	return ctx.Err()
}

func (p *Provider) Current(ctx context.Context) (focus.Target, error) {
	if p == nil {
		return focus.Target{}, focusUnavailable("read Accessibility focus", "focus provider is unavailable")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.initErr != nil {
		return focus.Target{}, p.initErr
	}
	if p.native == nil {
		return focus.Target{}, focusUnavailable("read Accessibility focus", "focus provider is closed")
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := waitForAttempt(ctx, attempt); err != nil {
			return focus.Target{}, err
		}
		var native C.wd_focus_target
		var message *C.char
		result := C.wd_focus_current(p.native, &native, &message)
		if result == C.WD_FOCUS_RESULT_OK {
			target := targetFromNative(&native)
			C.wd_focus_target_clear(&native)
			C.wd_focus_free(unsafe.Pointer(message))
			if err := ctx.Err(); err != nil {
				C.wd_focus_release(p.native, C.uint64_t(target.Token))
				return focus.Target{}, err
			}
			return target, nil
		}
		C.wd_focus_target_clear(&native)
		detail := takeMessage(message)
		if result != C.WD_FOCUS_RESULT_TRANSIENT || attempt == 1 {
			return focus.Target{}, nativeError(result, "read Accessibility focus", detail)
		}
	}
	panic("unreachable")
}

func (p *Provider) Same(ctx context.Context, captured focus.Target) (focus.Target, bool, error) {
	if p == nil {
		return focus.Target{}, false, focusUnavailable("compare Accessibility focus", "focus provider is unavailable")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.initErr != nil {
		return focus.Target{}, false, p.initErr
	}
	if p.native == nil {
		return focus.Target{}, false, focusUnavailable("compare Accessibility focus", "focus provider is closed")
	}
	if captured.Token == 0 {
		return focus.Target{}, false, focusUnavailable("compare Accessibility focus", "focus target is invalid")
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := waitForAttempt(ctx, attempt); err != nil {
			return focus.Target{}, false, err
		}
		var native C.wd_focus_target
		var same C.int
		var message *C.char
		result := C.wd_focus_same(p.native, C.uint64_t(captured.Token), &native, &same, &message)
		if result == C.WD_FOCUS_RESULT_OK {
			current := targetFromNative(&native)
			C.wd_focus_target_clear(&native)
			C.wd_focus_free(unsafe.Pointer(message))
			if err := ctx.Err(); err != nil {
				C.wd_focus_release(p.native, C.uint64_t(current.Token))
				return focus.Target{}, false, err
			}
			return current, same != 0, nil
		}
		C.wd_focus_target_clear(&native)
		detail := takeMessage(message)
		if result != C.WD_FOCUS_RESULT_TRANSIENT || attempt == 1 {
			return focus.Target{}, false, nativeError(result, "compare Accessibility focus", detail)
		}
	}
	panic("unreachable")
}

func (p *Provider) Release(target focus.Target) {
	if p == nil || target.Token == 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.native != nil {
		C.wd_focus_release(p.native, C.uint64_t(target.Token))
	}
}

func (p *Provider) Close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.native != nil {
		C.wd_focus_provider_destroy(p.native)
		p.native = nil
	}
	return nil
}

func waitForAttempt(ctx context.Context, attempt int) error {
	if attempt == 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(retryDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func targetFromNative(native *C.wd_focus_target) focus.Target {
	return focus.Target{
		Backend:        "accessibility",
		StableID:       goString(native.stable_id),
		AppID:          goString(native.app_id),
		AppName:        goString(native.app_name),
		PID:            int(native.pid),
		SecureField:    native.secure_field != 0,
		DegradedReason: goString(native.degraded_reason),
		Token:          uint64(native.token),
	}
}

func goString(value *C.char) string {
	if value == nil {
		return ""
	}
	return C.GoString(value)
}

func takeMessage(message *C.char) string {
	detail := goString(message)
	C.wd_focus_free(unsafe.Pointer(message))
	return detail
}

func nativeError(result C.int, operation, detail string) error {
	if detail == "" {
		detail = fmt.Sprintf("native result %d", int(result))
	}
	code := apperr.CodeFocusUnavailable
	if result == C.WD_FOCUS_RESULT_PERMISSION {
		code = apperr.CodePermissionAccessibilityDenied
	}
	return apperr.New(code, operation, fmt.Errorf("%s", detail))
}

func focusUnavailable(operation, detail string) error {
	return apperr.New(apperr.CodeFocusUnavailable, operation, fmt.Errorf("%s", detail))
}

var _ focus.Provider = (*Provider)(nil)
