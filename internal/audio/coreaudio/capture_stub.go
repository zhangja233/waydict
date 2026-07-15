//go:build !coreaudio || !cgo || !darwin

package coreaudio

import (
	"context"
	"time"

	"waydict/internal/audio"
	"waydict/internal/config"
)

type Capture struct {
	cfg config.Audio
}

func New(cfg config.Audio) (audio.Source, error) {
	return &Capture{cfg: cfg}, nil
}

func (*Capture) Start(context.Context) error { return audio.ErrUnavailable }
func (*Capture) Pause(context.Context) error { return nil }
func (*Capture) Stop(context.Context) error  { return nil }

func (*Capture) Read(ctx context.Context, _ []float32) (int, error) {
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-time.After(20 * time.Millisecond):
		return 0, audio.ErrUnavailable
	}
}

func (c *Capture) Stats() audio.Stats {
	return audio.Stats{
		Backend:    "coreaudio",
		SampleRate: c.cfg.SampleRate,
		LevelDBFS:  -120,
		DeviceID:   c.cfg.Device,
	}
}

type unavailableManager struct{}

func (unavailableManager) Devices(context.Context) ([]audio.Device, error) {
	return nil, audio.ErrUnavailable
}

func Manager() audio.DeviceManager { return unavailableManager{} }
func Check() error                 { return audio.ErrUnavailable }
