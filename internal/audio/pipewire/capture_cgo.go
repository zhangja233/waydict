//go:build pipewire && cgo && linux

package pipewire

/*
#cgo pkg-config: libpipewire-0.3
#cgo LDFLAGS: -lm
#include <stdlib.h>
#include "pipewire_capture.h"
*/
import "C"
import (
	"context"
	"fmt"
	"time"
	"unsafe"

	"waydict/internal/audio"
	"waydict/internal/config"
)

type Capture struct {
	ptr *C.sv_pw_capture
	cfg config.Audio
}

func New(cfg config.Audio) (*Capture, error) {
	target := C.CString(cfg.TargetObject)
	defer C.free(unsafe.Pointer(target))
	c := C.sv_pw_config{
		target_object:  target,
		sample_rate:    C.uint32_t(cfg.SampleRate),
		channels:       C.uint32_t(cfg.Channels),
		ring_frames:    C.uint32_t(cfg.SampleRate * cfg.RingSeconds),
		quantum_frames: C.uint32_t(cfg.SampleRate * cfg.QuantumMS / 1000),
	}
	var out *C.sv_pw_capture
	if rc := C.sv_pw_capture_new(&c, &out); rc != 0 {
		return nil, fmt.Errorf("pipewire capture init failed: %d", int(rc))
	}
	return &Capture{ptr: out, cfg: cfg}, nil
}

func (c *Capture) Start(context.Context) error {
	if rc := C.sv_pw_capture_start(c.ptr, 2000); rc != 0 {
		return fmt.Errorf("pipewire capture start failed: %d", int(rc))
	}
	return nil
}

func (c *Capture) Pause(context.Context) error {
	if rc := C.sv_pw_capture_pause(c.ptr); rc != 0 {
		return fmt.Errorf("pipewire capture pause failed: %d", int(rc))
	}
	return nil
}

func (c *Capture) Stop(context.Context) error {
	if rc := C.sv_pw_capture_stop(c.ptr); rc != 0 {
		return fmt.Errorf("pipewire capture stop failed: %d", int(rc))
	}
	return nil
}

func (c *Capture) Read(ctx context.Context, dst []float32) (int, error) {
	if len(dst) == 0 {
		return 0, nil
	}
	timeout := 20
	if deadline, ok := ctx.Deadline(); ok {
		ms := int(timeUntilMS(deadline))
		if ms < timeout {
			timeout = ms
		}
	}
	if timeout < 0 {
		timeout = 0
	}
	n := C.sv_pw_capture_read(c.ptr, (*C.float)(unsafe.Pointer(&dst[0])), C.uint32_t(len(dst)), C.int(timeout))
	if n < 0 {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		default:
		}
		return 0, fmt.Errorf("pipewire capture read failed: %d", int(n))
	}
	return int(n), nil
}

func (c *Capture) Stats() audio.Stats {
	var st C.sv_pw_stats
	C.sv_pw_capture_stats(c.ptr, &st)
	return audio.Stats{
		SampleRate: int(st.sample_rate),
		LevelDBFS:  float64(st.level_dbfs),
		Overruns:   uint64(st.overruns),
		Capturing:  st.capturing != 0,
	}
}

func (c *Capture) Close() {
	if c.ptr != nil {
		C.sv_pw_capture_free(c.ptr)
		c.ptr = nil
	}
}

func Check() error {
	cfg := config.Defaults().Audio
	c, err := New(cfg)
	if err != nil {
		return err
	}
	c.Close()
	return nil
}

func timeUntilMS(t time.Time) int64 {
	return time.Until(t).Milliseconds()
}
