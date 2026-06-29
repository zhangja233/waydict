//go:build !sherpa || !cgo

package sherpa

import (
	"context"
	"fmt"

	"sway-voice/internal/asr"
	"sway-voice/internal/config"
)

type Engine struct {
	cfg config.ASR
}

func New(cfg config.ASR) *Engine {
	return &Engine{cfg: cfg}
}

func (e *Engine) Name() string {
	return "sherpa-onnx"
}

func (e *Engine) Load(context.Context) error {
	return fmt.Errorf("sherpa support is not built in; rebuild with -tags sherpa and cgo enabled")
}

func (e *Engine) Close() error {
	return nil
}

func (e *Engine) Loaded() bool {
	return false
}

func (e *Engine) Transcribe(context.Context, asr.AudioSegment) (asr.Transcript, error) {
	return asr.Transcript{}, fmt.Errorf("sherpa support is not built in; rebuild with -tags sherpa and cgo enabled")
}
