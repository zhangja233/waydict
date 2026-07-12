//go:build !whispercpp || !cgo

package whispercpp

import (
	"context"
	"fmt"

	"waydict/internal/asr"
)

// Engine requires sequential use by a single worker.
type Engine struct {
	cfg Config
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

func (e *Engine) Load(context.Context) error {
	return buildSupportError()
}

func (e *Engine) Close() error {
	return nil
}

func (e *Engine) Loaded() bool {
	return false
}

func (e *Engine) Transcribe(context.Context, asr.AudioSegment) (asr.Transcript, error) {
	return asr.Transcript{}, buildSupportError()
}

func (e *Engine) ActiveBackend() (string, bool) {
	return "", false
}

func buildSupportError() error {
	return fmt.Errorf("whisper.cpp support is not built in; rebuild with -tags whispercpp and cgo enabled")
}
