package app

import (
	"context"
	"fmt"
	"time"

	sherpaasr "waydict/internal/asr/sherpa"
	"waydict/internal/audio"
	"waydict/internal/audio/pipewire"
	"waydict/internal/config"
	"waydict/internal/control"
	"waydict/internal/inject"
	svlog "waydict/internal/log"
	"waydict/internal/model"
	"waydict/internal/swayipc"
	"waydict/internal/vad"
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
	logger := svlog.New(cfg.Daemon.LogLevel, nil)
	logger.Info("daemon starting",
		"log_level", cfg.Daemon.LogLevel,
		"socket", cfg.Daemon.Socket,
		"audio_backend", cfg.Audio.Backend,
		"audio_sample_rate", cfg.Audio.SampleRate,
		"audio_quantum_ms", cfg.Audio.QuantumMS,
		"vad_engine", cfg.VAD.Engine,
		"asr_engine", cfg.ASR.Engine,
		"asr_model_type", cfg.ASR.ModelType,
		"asr_provider", cfg.ASR.Provider,
		"asr_threads", cfg.ASR.NumThreads,
		"preload_model", cfg.Daemon.PreloadModel,
		"redacted_transcripts", cfg.Daemon.RedactTranscriptsInLogs)
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
		logger.Info("asr preload start")
		if err := engine.Load(ctx); err != nil {
			return err
		}
		logger.Info("asr preload complete")
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
		Logger:         logger,
		Shutdown:       cancelDaemon,
	})
	err := control.NewServer(cfg.Daemon.Socket, application).Serve(ctx)
	if err != nil {
		logger.Error("daemon stopped with error", "error", err)
	} else {
		logger.Info("daemon stopped")
	}
	return err
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
