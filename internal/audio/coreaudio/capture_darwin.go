//go:build coreaudio && cgo && darwin

package coreaudio

/*
#cgo CFLAGS: -fobjc-arc -fblocks -mmacosx-version-min=13.0
#cgo LDFLAGS: -framework AVFoundation -framework AVFAudio -framework CoreAudio -framework AudioToolbox -framework Foundation -framework Accelerate
#include <stdlib.h>
#include "native.h"
#include "ring.h"
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"
	"unsafe"

	"waydict/internal/apperr"
	"waydict/internal/audio"
	"waydict/internal/config"
)

const (
	readWait       = 20 * time.Millisecond
	startWait      = 5 * time.Second
	eventPollWait  = 200 * time.Millisecond
	eventQueueSize = 128
)

type Capture struct {
	mu         sync.RWMutex
	ptr        *C.wd_ca_capture
	cfg        config.Audio
	stopped    bool
	events     chan audio.Event
	eventsDone chan struct{}
}

var (
	_ audio.Source      = (*Capture)(nil)
	_ audio.EventSource = (*Capture)(nil)
)

func New(cfg config.Audio) (audio.Source, error) {
	if cfg.SampleRate != 16000 || cfg.RingSeconds <= 0 || uint64(cfg.RingSeconds) > math.MaxUint32 ||
		cfg.QuantumMS <= 0 || uint64(cfg.QuantumMS) > math.MaxUint32 {
		return nil, apperr.New(apperr.CodeAudioStartFailed, "initialize CoreAudio capture", fmt.Errorf("output must be 16000 Hz with positive, bounded ring and quantum values"))
	}
	var ptr *C.wd_ca_capture
	var detail *C.char
	status := C.wd_ca_capture_create(C.uint32_t(cfg.SampleRate), C.uint32_t(cfg.RingSeconds), &ptr, &detail)
	if status != C.WD_CA_STATUS_OK {
		return nil, nativeError(status, "initialize CoreAudio capture", consumeDetail(detail))
	}
	capture := &Capture{
		ptr:        ptr,
		cfg:        cfg,
		events:     make(chan audio.Event, eventQueueSize),
		eventsDone: make(chan struct{}),
	}
	go capture.pumpEvents()
	return capture, nil
}

func (c *Capture) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.IndexByte(c.cfg.Device, 0) >= 0 {
		return apperr.New(apperr.CodeAudioDeviceNotFound, "start CoreAudio capture", errors.New("device UID contains NUL"))
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ptr == nil || c.stopped {
		return audio.ErrUnavailable
	}
	device := C.CString(c.cfg.Device)
	defer C.free(unsafe.Pointer(device))
	var detail *C.char
	status := C.wd_ca_capture_start(c.ptr, device, C.uint32_t(c.cfg.QuantumMS), C.uint32_t(startWait/time.Millisecond), &detail)
	if status != C.WD_CA_STATUS_OK {
		return nativeError(status, "start CoreAudio capture", consumeDetail(detail))
	}
	if err := ctx.Err(); err != nil {
		_ = c.pauseLocked()
		return err
	}
	return nil
}

func (c *Capture) Pause(ctx context.Context) error {
	_ = ctx
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ptr == nil || c.stopped {
		return nil
	}
	return c.pauseLocked()
}

func (c *Capture) pauseLocked() error {
	var detail *C.char
	status := C.wd_ca_capture_pause(c.ptr, &detail)
	if status != C.WD_CA_STATUS_OK {
		if status == C.WD_CA_STATUS_TEARDOWN_TIMEOUT || status == C.WD_CA_STATUS_START_FAILED {
			c.stopped = true
		}
		return nativeError(status, "pause CoreAudio capture", consumeDetail(detail))
	}
	return nil
}

func (c *Capture) Stop(ctx context.Context) error {
	_ = ctx
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ptr == nil || c.stopped {
		return nil
	}
	var detail *C.char
	status := C.wd_ca_capture_stop(c.ptr, &detail)
	c.stopped = true
	if status != C.WD_CA_STATUS_OK {
		return nativeError(status, "stop CoreAudio capture", consumeDetail(detail))
	}
	return nil
}

func (c *Capture) Read(ctx context.Context, dst []float32) (int, error) {
	if len(dst) == 0 {
		c.mu.RLock()
		stopped := c.ptr == nil || c.stopped
		c.mu.RUnlock()
		if stopped {
			return 0, audio.ErrUnavailable
		}
		return 0, nil
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	timeout := readWait
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining < timeout {
			timeout = remaining
		}
	}
	if timeout < 0 {
		timeout = 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.ptr == nil || c.stopped {
		return 0, audio.ErrUnavailable
	}
	var count C.size_t
	var detail *C.char
	status := C.wd_ca_capture_read(
		c.ptr,
		(*C.float)(unsafe.Pointer(&dst[0])),
		C.size_t(len(dst)),
		C.uint32_t(timeout/time.Millisecond),
		&count,
		&detail,
	)
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if status != C.WD_CA_STATUS_OK {
		return 0, nativeError(status, "read CoreAudio capture", consumeDetail(detail))
	}
	return int(count), nil
}

func (c *Capture) Stats() audio.Stats {
	c.mu.RLock()
	defer c.mu.RUnlock()
	stats := audio.Stats{
		Backend:    "coreaudio",
		SampleRate: 16000,
		LevelDBFS:  -120,
		DeviceID:   c.cfg.Device,
	}
	if c.ptr == nil {
		return stats
	}
	var native C.wd_ca_stats
	if C.wd_ca_capture_stats(c.ptr, &native, nil) != C.WD_CA_STATUS_OK {
		return stats
	}
	stats.SampleRate = int(native.sample_rate)
	stats.LevelDBFS = float64(native.level_dbfs)
	stats.Overruns = uint64(native.overruns)
	stats.Capturing = bool(native.capturing)
	if native.device_uid[0] != 0 {
		stats.DeviceID = C.GoString(&native.device_uid[0])
	}
	if native.device_name[0] != 0 {
		stats.DeviceName = C.GoString(&native.device_name[0])
	}
	seconds := float64(native.input_latency_seconds)
	if seconds > 0 && !math.IsNaN(seconds) && !math.IsInf(seconds, 0) {
		stats.InputLatency = time.Duration(seconds * float64(time.Second))
	}
	return stats
}

func (c *Capture) Events() <-chan audio.Event {
	return c.events
}

func (c *Capture) Close() {
	_ = c.Stop(context.Background())
	<-c.eventsDone
	c.mu.Lock()
	if c.ptr != nil {
		C.wd_ca_capture_destroy(c.ptr)
		c.ptr = nil
	}
	c.mu.Unlock()
}

func (c *Capture) pumpEvents() {
	defer close(c.eventsDone)
	defer close(c.events)
	for {
		var event C.wd_ca_event
		var detail *C.char
		status := C.wd_ca_capture_next_event(c.ptr, C.uint32_t(eventPollWait/time.Millisecond), &event, &detail)
		if status == C.WD_CA_STATUS_STOPPED {
			return
		}
		if status != C.WD_CA_STATUS_OK {
			err := nativeError(status, "read CoreAudio event", consumeDetail(detail))
			c.emitEvent(audio.Event{Kind: audio.EventFormatChanged, At: time.Now(), Err: err})
			continue
		}
		if event.kind == C.WD_CA_EVENT_NONE {
			continue
		}
		kind := eventKind(event.kind)
		var err error
		if event.status != C.WD_CA_STATUS_OK {
			err = nativeError(C.int(event.status), "handle CoreAudio device event", "")
		}
		c.emitEvent(audio.Event{
			Kind:     kind,
			DeviceID: C.GoString(&event.device_uid[0]),
			At:       time.Now(),
			Err:      err,
		})
	}
}

func (c *Capture) emitEvent(event audio.Event) {
	select {
	case c.events <- event:
	default:
		if event.Kind != audio.EventOverrun {
			select {
			case <-c.events:
			default:
			}
			select {
			case c.events <- event:
			default:
			}
		}
	}
}

func eventKind(kind C.wd_ca_event_kind) audio.EventKind {
	switch kind {
	case C.WD_CA_EVENT_DEFAULT_CHANGED:
		return audio.EventDefaultChanged
	case C.WD_CA_EVENT_DEVICE_REMOVED:
		return audio.EventDeviceRemoved
	case C.WD_CA_EVENT_DEVICE_ADDED:
		return audio.EventDeviceAdded
	case C.WD_CA_EVENT_FORMAT_CHANGED:
		return audio.EventFormatChanged
	case C.WD_CA_EVENT_OVERRUN:
		return audio.EventOverrun
	default:
		return audio.EventFormatChanged
	}
}

func nativeError(status C.int, operation, detail string) error {
	if detail == "" {
		detail = fmt.Sprintf("native status %d", int(status))
	}
	appError := &apperr.Error{Operation: operation, Err: errors.New(detail)}
	switch status {
	case C.WD_CA_STATUS_PERMISSION:
		appError.Code = apperr.CodePermissionMicrophoneDenied
	case C.WD_CA_STATUS_DEVICE_NOT_FOUND:
		appError.Code = apperr.CodeAudioDeviceNotFound
	case C.WD_CA_STATUS_DEVICE_DISCONNECTED:
		appError.Code = apperr.CodeAudioDeviceDisconnected
		appError.Retryable = true
	case C.WD_CA_STATUS_DEVICE_CHANGED:
		appError.Code = apperr.CodeAudioDeviceDisconnected
		appError.Retryable = true
	case C.WD_CA_STATUS_FORMAT_CHANGED, C.WD_CA_STATUS_CONVERTER_FAILED,
		C.WD_CA_STATUS_START_FAILED, C.WD_CA_STATUS_START_TIMEOUT,
		C.WD_CA_STATUS_TEARDOWN_TIMEOUT:
		appError.Code = apperr.CodeAudioStartFailed
		appError.Retryable = true
	case C.WD_CA_STATUS_BACKEND_UNAVAILABLE:
		appError.Code = apperr.CodeAudioBackendUnavailable
		appError.Retryable = true
	case C.WD_CA_STATUS_STOPPED, C.WD_CA_STATUS_NO_MEMORY:
		appError.Code = apperr.CodeAudioBackendUnavailable
	case C.WD_CA_STATUS_INVALID:
		appError.Code = apperr.CodeAudioStartFailed
	default:
		appError.Code = apperr.CodeAudioBackendUnavailable
	}
	return appError
}

func consumeDetail(detail *C.char) string {
	if detail == nil {
		return ""
	}
	value := C.GoString(detail)
	C.wd_ca_free(unsafe.Pointer(detail))
	return value
}

func testConvert(interleaved []float32, frames, channels int, sampleRate float64) ([]float32, error) {
	if len(interleaved) == 0 {
		return nil, errors.New("empty conversion fixture")
	}
	var output *C.float
	var outputFrames C.uint32_t
	var detail *C.char
	status := C.wd_ca_test_convert(
		(*C.float)(unsafe.Pointer(&interleaved[0])),
		C.uint32_t(frames),
		C.uint32_t(channels),
		C.double(sampleRate),
		&output,
		&outputFrames,
		&detail,
	)
	if status != C.WD_CA_STATUS_OK {
		return nil, nativeError(status, "convert CoreAudio test fixture", consumeDetail(detail))
	}
	defer C.wd_ca_free(unsafe.Pointer(output))
	native := unsafe.Slice((*float32)(unsafe.Pointer(output)), int(outputFrames))
	return append([]float32(nil), native...), nil
}

func testRing() int {
	return int(C.wd_ca_ring_self_test())
}

func testTapGate() int {
	return int(C.wd_ca_test_tap_gate())
}

func testTeardownTimeout() (int, uint32) {
	var elapsed C.uint32_t
	result := C.wd_ca_test_teardown_timeout(&elapsed)
	return int(result), uint32(elapsed)
}
