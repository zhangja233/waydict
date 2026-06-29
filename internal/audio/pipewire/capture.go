//go:build !pipewire || !cgo || !linux

package pipewire

import (
	"context"

	"waydict/internal/audio"
	"waydict/internal/config"
)

type Capture struct {
	cfg config.Audio
}

func New(cfg config.Audio) (*Capture, error) {
	return &Capture{cfg: cfg}, nil
}

func (c *Capture) Start(context.Context) error {
	return audio.ErrUnavailable
}

func (c *Capture) Pause(context.Context) error {
	return nil
}

func (c *Capture) Stop(context.Context) error {
	return nil
}

func (c *Capture) Read(context.Context, []float32) (int, error) {
	return 0, audio.ErrUnavailable
}

func (c *Capture) Stats() audio.Stats {
	return audio.Stats{SampleRate: c.cfg.SampleRate, LevelDBFS: -120, Capturing: false}
}

func Check() error {
	return audio.ErrUnavailable
}
