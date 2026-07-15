//go:build darwin && cgo

package quartz

/*
#cgo CFLAGS: -fobjc-arc -mmacosx-version-min=13.0
#cgo LDFLAGS: -framework ApplicationServices -framework Carbon -framework CoreGraphics
#include "native.h"
*/
import "C"

import (
	"context"
	"fmt"
	"time"
	"unicode/utf16"
	"unsafe"

	"waydict/internal/apperr"
	"waydict/internal/config"
	"waydict/internal/inject"
)

type Injector struct {
	delayMS   int
	timeoutMS int
}

func New(cfg config.Injection) *Injector {
	return &Injector{delayMS: cfg.KeyDelayMS, timeoutMS: cfg.TimeoutMS}
}

func (q *Injector) Backend() string { return "quartz" }

func (q *Injector) Available(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if C.wd_quartz_available() == 0 {
		return permissionError("check Quartz injection")
	}
	return ctx.Err()
}

func (q *Injector) Inject(parent context.Context, request inject.Request) error {
	if request.Text == "" {
		return nil
	}
	if request.Target.Focus.SecureField {
		return apperr.New(apperr.CodeSecureField, "inject with Quartz", fmt.Errorf("target is a secure text field"))
	}
	if request.ValidateTarget == nil {
		return apperr.New(apperr.CodeInjectionFailed, "inject with Quartz", fmt.Errorf("target validator is required"))
	}

	ctx, cancel := q.operationContext(parent, request.Deadline)
	defer cancel()
	chunks, err := chunkText(request.Text)
	if err != nil {
		return injectionError("prepare Quartz injection", 0, err)
	}
	if err := ctx.Err(); err != nil {
		return injectionError("inject with Quartz", 0, err)
	}
	if err := q.Available(ctx); err != nil {
		return err
	}

	var transaction *C.wd_quartz_transaction
	if result := C.wd_quartz_transaction_create(&transaction); result != C.WD_QUARTZ_RESULT_OK {
		return injectionError("create Quartz event source", 0, fmt.Errorf("native result %d", int(result)))
	}
	defer C.wd_quartz_transaction_destroy(transaction)

	delay := time.Duration(q.delayMS) * time.Millisecond
	if request.KeyDelay > 0 {
		delay = request.KeyDelay
	}
	submitted := 0
	for index, chunk := range chunks {
		if err := ctx.Err(); err != nil {
			return injectionError("inject with Quartz", submitted, err)
		}
		if err := request.ValidateTarget(ctx, request.Target.Focus); err != nil {
			return err
		}
		if C.wd_quartz_available() == 0 {
			return permissionError(fmt.Sprintf("continue Quartz injection after %d event pairs", submitted))
		}

		var result C.int
		switch chunk.kind {
		case chunkUnicode:
			units := utf16.Encode([]rune(chunk.text))
			result = C.wd_quartz_post_unicode(
				transaction,
				(*C.uint16_t)(unsafe.Pointer(&units[0])),
				C.size_t(len(units)),
			)
		case chunkReturn:
			result = C.wd_quartz_post_key(transaction, C.wd_quartz_return_keycode())
		case chunkTab:
			result = C.wd_quartz_post_key(transaction, C.wd_quartz_tab_keycode())
		default:
			result = C.WD_QUARTZ_RESULT_INVALID
		}
		if result != C.WD_QUARTZ_RESULT_OK {
			return injectionError("construct Quartz keyboard events", submitted, fmt.Errorf("native result %d", int(result)))
		}
		submitted++
		if delay > 0 && index+1 < len(chunks) {
			if err := waitDelay(ctx, delay); err != nil {
				return injectionError("inject with Quartz", submitted, err)
			}
		}
	}
	return nil
}

func (q *Injector) operationContext(parent context.Context, requested time.Time) (context.Context, context.CancelFunc) {
	deadline := requested
	if q.timeoutMS > 0 {
		configured := time.Now().Add(time.Duration(q.timeoutMS) * time.Millisecond)
		if deadline.IsZero() || configured.Before(deadline) {
			deadline = configured
		}
	}
	if parentDeadline, ok := parent.Deadline(); ok && (deadline.IsZero() || parentDeadline.Before(deadline)) {
		return context.WithCancel(parent)
	}
	if deadline.IsZero() {
		return context.WithCancel(parent)
	}
	return context.WithDeadline(parent, deadline)
}

func waitDelay(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func permissionError(operation string) error {
	return apperr.New(apperr.CodePermissionAccessibilityDenied, operation, fmt.Errorf("Accessibility or event-post permission is not granted"))
}

func injectionError(operation string, submitted int, cause error) error {
	return apperr.New(apperr.CodeInjectionFailed, operation, fmt.Errorf("after %d event pairs: %w", submitted, cause))
}

var _ inject.Injector = (*Injector)(nil)
