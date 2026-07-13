package app

import (
	"context"
	"fmt"
	"os"
	"time"

	"waydict/internal/asr"
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
	NewWhisper       func(modelPath string, device, threads int, useGPU bool, initialPrompt string) (asr.Engine, error)
	ProbeGPU         func() (string, error)
}

func RunDaemonWithOptions(ctx context.Context, cfg config.Config, opts DaemonOptions) error {
	ctx, cancelDaemon := context.WithCancel(ctx)
	defer cancelDaemon()
	if err := cfg.Validate(); err != nil {
		return err
	}
	engine, resolution, err := resolveDaemonASR(cfg, opts)
	if err != nil {
		return err
	}
	logger := svlog.New(cfg.Daemon.LogLevel, nil)
	checkModel := func(engine string) error {
		// Pin to the engine being validated so an auto config cannot satisfy a
		// sherpa check with a whisper model (or vice versa).
		pinned := cfg
		if engine != "" {
			pinned.ASR.Engine = engine
			if engine == asr.EngineSherpa {
				pinned.ASR.Provider = asr.ProviderCPU
			}
		}
		res := model.CheckConfig(pinned, model.CheckOptions{StrictSizes: true})
		if !res.OK {
			return fmt.Errorf("model validation failed: %v", res.Errors)
		}
		return nil
	}
	if resolution.Engine == asr.EngineSherpa {
		if err := validateResolvedSherpaConfig(cfg); err != nil {
			return err
		}
		if cfg.Daemon.PreloadModel {
			if err := checkModel(asr.EngineSherpa); err != nil {
				return err
			}
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
	application := New(ctx, cfg, Dependencies{
		SourceFactory: func() (audio.Source, error) {
			return pipewire.New(cfg.Audio)
		},
		ModelChecker:   checkModel,
		ConfigReloader: reloadConfig(opts),
		Segmenter:      vad.NewSegmenter(cfg.VAD, cfg.Audio.SampleRate),
		Engine:         engine,
		ASRResolution:  resolution,
		ASRFallback: func() (asr.Engine, asr.Resolution, error) {
			fallbackCfg := cfg
			fallbackCfg.ASR.Engine = asr.EngineSherpa
			fallbackCfg.ASR.Provider = asr.ProviderCPU
			return resolveDaemonASR(fallbackCfg, opts)
		},
		Injector: inject.NewWtype(cfg.Injection),
		Focus:    focus,
		Logger:   logger,
		Shutdown: cancelDaemon,
	})
	if cfg.Daemon.PreloadModel {
		if err := application.loadASR(ctx, 0); err != nil {
			return err
		}
	}
	asrStatus := application.Status(ctx).ASR
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
		"asr_resolved_engine", asrStatus.ResolvedEngine,
		"asr_resolved_provider", asrStatus.ResolvedProvider,
		"asr_gpu_name", asrStatus.GPUName,
		"asr_fallback_reason", asrStatus.FallbackReason,
		"asr_threads", cfg.ASR.NumThreads,
		"preload_model", cfg.Daemon.PreloadModel,
		"redacted_transcripts", cfg.Daemon.RedactTranscriptsInLogs)
	err = control.NewServer(cfg.Daemon.Socket, application).Serve(ctx)
	if cerr := application.CloseASR(); cerr != nil {
		logger.Warn("asr close failed", "error", cerr)
	}
	if err != nil {
		logger.Error("daemon stopped with error", "error", err)
	} else {
		logger.Info("daemon stopped")
	}
	return err
}

func resolveDaemonASR(cfg config.Config, opts DaemonOptions) (asr.Engine, asr.Resolution, error) {
	provider := cfg.ASR.Provider
	if provider == "" && cfg.ASR.Engine == asr.EngineWhisper {
		provider = asr.ProviderVulkan
	}
	sherpaCfg := cfg.ASR
	sherpaCfg.Provider = asr.ProviderCPU
	deps := asr.ResolverDeps{
		NewSherpa: func() asr.Engine { return sherpaasr.New(sherpaCfg) },
		ProbeGPU:  opts.ProbeGPU,
		WhisperModelPath: func() (string, error) {
			path := cfg.WhisperModelPath()
			info, err := os.Stat(path)
			if err != nil {
				return "", err
			}
			if !info.Mode().IsRegular() {
				return "", fmt.Errorf("%s is not a regular file", path)
			}
			return path, nil
		},
	}
	if hook := opts.NewWhisper; hook != nil {
		deps.NewWhisper = func(modelPath string, device int, useGPU bool) (asr.Engine, error) {
			return hook(modelPath, device, cfg.ASR.NumThreads, useGPU, config.WhisperInitialPrompt(cfg.ASR.Vocabulary))
		}
	}
	engine, resolution, err := asr.Resolve(cfg.ASR.Engine, provider, cfg.ASR.GPUDevice, deps)
	if err != nil {
		return nil, asr.Resolution{}, err
	}
	if resolution.Engine == asr.EngineSherpa {
		if err := validateResolvedSherpaConfig(cfg); err != nil {
			_ = engine.Close()
			return nil, resolution, err
		}
	}
	return engine, resolution, nil
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
