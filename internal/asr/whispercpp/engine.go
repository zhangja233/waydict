//go:build whispercpp && cgo

package whispercpp

/*
#cgo pkg-config: whisper
#include <stdlib.h>
#include "bridge.h"
*/
import "C"

import (
	"context"
	"fmt"
	"math"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"waydict/internal/asr"
)

const sampleRate = 16000

var (
	nativeMu        sync.Mutex
	activeCaptureMu sync.Mutex
	activeCapture   *logCapture
)

type logCapture struct {
	mu       sync.Mutex
	detector backendDetector
	lastLine string
}

func (c *logCapture) reset() {
	c.mu.Lock()
	c.detector = backendDetector{}
	c.lastLine = ""
	c.mu.Unlock()
}

func (c *logCapture) observe(text string) {
	c.mu.Lock()
	c.detector.observe(text)
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			c.lastLine = line
		}
	}
	c.mu.Unlock()
}

func (c *logCapture) backend() (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.detector.backend()
}

func (c *logCapture) last() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastLine
}

// Engine requires sequential use by a single worker. loaded is atomic because
// the control server reads Loaded()/ActiveBackend() while the worker loads.
type Engine struct {
	cfg    Config
	ctx    *C.struct_whisper_context
	logs   logCapture
	loaded atomic.Bool
}

var (
	_ asr.Engine          = (*Engine)(nil)
	_ asr.BackendReporter = (*Engine)(nil)
)

func New(cfg Config) (*Engine, error) {
	cfg, err := normalizeConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &Engine{cfg: cfg}, nil
}

func (e *Engine) Name() string {
	return "whisper-cpp"
}

func (e *Engine) Loaded() bool {
	return e.loaded.Load()
}

func (e *Engine) ActiveBackend() (string, bool) {
	if !e.loaded.Load() {
		return "", false
	}
	return e.logs.backend()
}

func (e *Engine) Load(ctx context.Context) error {
	if e.loaded.Load() {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	nativeMu.Lock()
	defer nativeMu.Unlock()
	e.logs.reset()
	beginCapture(&e.logs)
	defer endCapture(&e.logs)
	C.waydict_whisper_log_install()

	modelPath := C.CString(e.cfg.ModelPath)
	defer C.free(unsafe.Pointer(modelPath))
	e.ctx = C.waydict_whisper_init(modelPath, C.bool(e.cfg.UseGPU), C.int(e.cfg.Device))
	if e.ctx == nil {
		if detail := e.logs.last(); detail != "" {
			return fmt.Errorf("failed to load whisper model %q: %s", e.cfg.ModelPath, detail)
		}
		return fmt.Errorf("failed to load whisper model %q", e.cfg.ModelPath)
	}
	e.loaded.Store(true)
	return nil
}

func (e *Engine) Close() error {
	if e.ctx == nil {
		e.loaded.Store(false)
		return nil
	}
	nativeMu.Lock()
	beginCapture(&e.logs)
	C.whisper_free(e.ctx)
	endCapture(&e.logs)
	nativeMu.Unlock()
	e.ctx = nil
	e.loaded.Store(false)
	e.logs.reset()
	return nil
}

func (e *Engine) Transcribe(ctx context.Context, segment asr.AudioSegment) (asr.Transcript, error) {
	if segment.SampleRate != sampleRate {
		return asr.Transcript{}, fmt.Errorf("whisper.cpp requires %d Hz audio, got %d", sampleRate, segment.SampleRate)
	}
	if len(segment.Samples) == 0 {
		return emptyTranscript(segment, 0), nil
	}
	if len(segment.Samples) > math.MaxInt32 {
		return asr.Transcript{}, fmt.Errorf("audio segment has too many samples: %d", len(segment.Samples))
	}
	if err := e.Load(ctx); err != nil {
		return asr.Transcript{}, err
	}
	if err := ctx.Err(); err != nil {
		return asr.Transcript{}, err
	}

	nativeMu.Lock()
	defer nativeMu.Unlock()
	beginCapture(&e.logs)
	defer endCapture(&e.logs)

	// ctx is only honored between calls: the native decode blocks until done,
	// matching the sherpa engine. VAD bounds segment length, so the window is small.
	start := time.Now()
	result := C.waydict_whisper_full(
		e.ctx,
		(*C.float)(unsafe.Pointer(&segment.Samples[0])),
		C.int(len(segment.Samples)),
		C.int(e.cfg.NumThreads),
	)
	runtime.KeepAlive(segment.Samples)
	decodeDuration := time.Since(start)
	if result != 0 {
		return asr.Transcript{}, fmt.Errorf("whisper.cpp transcription failed with code %d", int(result))
	}

	var text strings.Builder
	var tokens []string
	for i := 0; i < int(C.whisper_full_n_segments(e.ctx)); i++ {
		text.WriteString(C.GoString(C.whisper_full_get_segment_text(e.ctx, C.int(i))))
		for j := 0; j < int(C.whisper_full_n_tokens(e.ctx, C.int(i))); j++ {
			id := C.whisper_full_get_token_id(e.ctx, C.int(i), C.int(j))
			if id < C.whisper_token_eot(e.ctx) {
				tokens = append(tokens, C.GoString(C.whisper_full_get_token_text(e.ctx, C.int(i), C.int(j))))
			}
		}
	}

	audioDuration := segment.Duration
	if audioDuration <= 0 {
		audioDuration = time.Duration(len(segment.Samples)) * time.Second / sampleRate
	}
	rtf := decodeDuration.Seconds() / audioDuration.Seconds()
	transcriptText := text.String()
	return asr.Transcript{
		SegmentID:       segment.ID,
		Text:            transcriptText,
		Tokens:          tokens,
		TokenTimestamps: nil,
		StartedAt:       segment.StartedAt,
		AudioDuration:   audioDuration,
		DecodeDuration:  decodeDuration,
		RealTimeFactor:  rtf,
		Empty:           strings.TrimSpace(transcriptText) == "",
	}, nil
}

func beginCapture(capture *logCapture) {
	activeCaptureMu.Lock()
	activeCapture = capture
	activeCaptureMu.Unlock()
}

func endCapture(capture *logCapture) {
	activeCaptureMu.Lock()
	if activeCapture == capture {
		activeCapture = nil
	}
	activeCaptureMu.Unlock()
}

func emptyTranscript(segment asr.AudioSegment, decodeDuration time.Duration) asr.Transcript {
	audioDuration := segment.Duration
	if audioDuration <= 0 && segment.SampleRate > 0 {
		audioDuration = time.Duration(len(segment.Samples)) * time.Second / time.Duration(segment.SampleRate)
	}
	return asr.Transcript{
		SegmentID:      segment.ID,
		StartedAt:      segment.StartedAt,
		AudioDuration:  audioDuration,
		DecodeDuration: decodeDuration,
		Empty:          true,
	}
}
