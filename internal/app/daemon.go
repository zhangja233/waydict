package app

import (
	"context"
	"fmt"
	"time"

	sherpaasr "sway-voice/internal/asr/sherpa"
	"sway-voice/internal/audio/pipewire"
	"sway-voice/internal/config"
	"sway-voice/internal/control"
	"sway-voice/internal/inject"
	"sway-voice/internal/swayipc"
	"sway-voice/internal/vad"
)

func RunDaemon(ctx context.Context, cfg config.Config) error {
	ctx, cancelDaemon := context.WithCancel(ctx)
	defer cancelDaemon()
	if err := cfg.Validate(); err != nil {
		return err
	}
	if cfg.Daemon.PreloadModel {
		if err := cfg.ValidateModelReadable(); err != nil {
			return fmt.Errorf("model validation failed: %w", err)
		}
	}
	focus := swayipc.New(cfg.Sway.Socket)
	if cfg.Sway.RequireSway {
		fctx, cancel := context.WithTimeout(ctx, time.Second)
		err := focus.Available(fctx)
		cancel()
		if err != nil {
			return fmt.Errorf("sway unavailable: %w", err)
		}
	}
	capture, err := pipewire.New(cfg.Audio)
	if err != nil {
		return err
	}
	engine := sherpaasr.New(cfg.ASR)
	if cfg.Daemon.PreloadModel {
		if err := engine.Load(ctx); err != nil {
			return err
		}
	}
	application := New(ctx, cfg, Dependencies{
		Source:    capture,
		Segmenter: vad.NewSegmenter(cfg.VAD, cfg.Audio.SampleRate),
		Engine:    engine,
		Injector:  inject.NewWtype(cfg.Injection),
		Focus:     focus,
		Shutdown:  cancelDaemon,
	})
	return control.NewServer(cfg.Daemon.Socket, application).Serve(ctx)
}
