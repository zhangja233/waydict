package app

import (
	"context"
	"fmt"
	"time"

	sherpaasr "sway-voice/internal/asr/sherpa"
	"sway-voice/internal/audio"
	"sway-voice/internal/audio/pipewire"
	"sway-voice/internal/config"
	"sway-voice/internal/control"
	"sway-voice/internal/inject"
	svlog "sway-voice/internal/log"
	"sway-voice/internal/model"
	"sway-voice/internal/swayipc"
	"sway-voice/internal/vad"
)

func RunDaemon(ctx context.Context, cfg config.Config) error {
	return RunDaemonWithOptions(ctx, cfg, DaemonOptions{})
}

type DaemonOptions struct {
	ConfigPath       string
	LogLevelOverride string
}

func RunDaemonWithOptions(ctx context.Context, cfg config.Config, opts DaemonOptions) error {
	ctx, cancelDaemon := context.WithCancel(ctx)
	defer cancelDaemon()
	if err := cfg.Validate(); err != nil {
		return err
	}
	checkModel := func() error {
		res := model.CheckConfig(cfg, model.CheckOptions{StrictSizes: true})
		if !res.OK {
			return fmt.Errorf("model validation failed: %v", res.Errors)
		}
		return nil
	}
	if cfg.Daemon.PreloadModel {
		if err := checkModel(); err != nil {
			return err
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
	engine := sherpaasr.New(cfg.ASR)
	if cfg.Daemon.PreloadModel {
		if err := engine.Load(ctx); err != nil {
			return err
		}
	}
	application := New(ctx, cfg, Dependencies{
		SourceFactory: func() (audio.Source, error) {
			return pipewire.New(cfg.Audio)
		},
		ModelChecker:   checkModel,
		ConfigReloader: reloadConfig(opts),
		Segmenter:      vad.NewSegmenter(cfg.VAD, cfg.Audio.SampleRate),
		Engine:         engine,
		Injector:       inject.NewWtype(cfg.Injection),
		Focus:          focus,
		Logger:         svlog.New(cfg.Daemon.LogLevel, nil),
		Shutdown:       cancelDaemon,
	})
	return control.NewServer(cfg.Daemon.Socket, application).Serve(ctx)
}

func reloadConfig(opts DaemonOptions) func(context.Context) (config.Config, error) {
	return func(ctx context.Context) (config.Config, error) {
		select {
		case <-ctx.Done():
			return config.Config{}, ctx.Err()
		default:
		}
		cfg, err := config.Load(opts.ConfigPath)
		if err != nil {
			return config.Config{}, err
		}
		if opts.LogLevelOverride != "" {
			cfg.Daemon.LogLevel = opts.LogLevelOverride
		}
		return cfg, nil
	}
}
