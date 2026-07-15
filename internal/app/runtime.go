package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"waydict/internal/apperr"
	"waydict/internal/asr"
	sherpaasr "waydict/internal/asr/sherpa"
	"waydict/internal/audio"
	"waydict/internal/config"
	"waydict/internal/control"
	"waydict/internal/focus"
	"waydict/internal/inject"
	svlog "waydict/internal/log"
	"waydict/internal/model"
	"waydict/internal/permissions"
	"waydict/internal/vad"
	"waydict/pkg/api"
)

type PlatformDependencies struct {
	Name             string
	NewSource        func(config.Audio) (audio.Source, error)
	NewInjector      func(config.Injection) inject.Injector
	NewFocusProvider func(config.Focus) focus.Provider
	PermissionSource permissions.Source
	DeviceManager    audio.DeviceManager
}

type WhisperFactory func(modelPath, provider string, device, threads int) (asr.Engine, error)

type RuntimeOptions struct {
	ConfigPath           string
	LogLevelOverride     string
	Platform             PlatformDependencies
	NewWhisper           WhisperFactory
	ProbeAccelerator     func(provider string, device int) (asr.Accelerator, error)
	AllowDegradedStartup bool
	Shutdown             func()
}

type Runtime struct {
	App      *App
	Server   *control.Server
	Config   config.Config
	Platform PlatformDependencies

	mu           sync.Mutex
	ctx          context.Context
	cancel       context.CancelFunc
	opts         RuntimeOptions
	logger       *slog.Logger
	modelChecker func(string) error
	closed       bool
}

func NewRuntime(ctx context.Context, cfg config.Config, opts RuntimeOptions) (*Runtime, error) {
	if opts.LogLevelOverride != "" {
		cfg.Daemon.LogLevel = opts.LogLevelOverride
	}
	if err := cfg.Validate(); err != nil {
		return nil, apperr.New(apperr.CodeConfigInvalid, "validate config", err)
	}
	if opts.Platform.Name == "" {
		opts.Platform.Name = "unknown"
	}
	runtimeCtx, cancel := context.WithCancel(ctx)
	logger := svlog.New(cfg.Daemon.LogLevel, nil)
	r := &Runtime{
		Config:   cfg,
		Platform: opts.Platform,
		ctx:      runtimeCtx,
		cancel:   cancel,
		opts:     opts,
		logger:   logger,
	}
	fail := func(err error) (*Runtime, error) {
		cancel()
		return nil, err
	}

	engine, resolution, resolutionErr := resolveRuntimeASR(cfg, opts)
	if resolutionErr != nil && !opts.AllowDegradedStartup {
		return fail(resolutionErr)
	}
	r.modelChecker = runtimeModelChecker(cfg)
	if resolutionErr == nil && resolution.Engine == asr.EngineSherpa {
		if err := validateResolvedSherpaConfig(cfg); err != nil {
			err = apperr.New(apperr.CodeConfigInvalid, "validate resolved ASR", err)
			if !opts.AllowDegradedStartup {
				_ = engine.Close()
				return fail(err)
			}
			resolutionErr = err
		}
		if resolutionErr == nil && cfg.Daemon.PreloadModel {
			if err := r.modelChecker(asr.EngineSherpa); err != nil {
				if !opts.AllowDegradedStartup {
					_ = engine.Close()
					return fail(err)
				}
				resolutionErr = err
			}
		}
	}

	var provider focus.Provider
	var focusErr error
	if cfg.Focus.Enabled || cfg.Focus.Required {
		if opts.Platform.NewFocusProvider != nil {
			provider = opts.Platform.NewFocusProvider(cfg.Focus)
		}
		if provider == nil {
			focusErr = apperr.New(apperr.CodeFocusUnavailable, "construct focus provider", fmt.Errorf("focus provider factory is unavailable"))
		} else if cfg.Focus.Required {
			focusCtx, focusCancel := context.WithTimeout(runtimeCtx, time.Second)
			focusErr = provider.Available(focusCtx)
			focusCancel()
		}
	}
	if focusErr != nil && !opts.AllowDegradedStartup {
		if engine != nil {
			_ = engine.Close()
		}
		return fail(normalizeError(apperr.CodeFocusUnavailable, "check focus provider", focusErr))
	}
	if focusErr != nil && provider == nil {
		provider = runtimeUnavailableFocus{name: opts.Platform.Name, err: focusErr}
	}

	var injector inject.Injector
	if opts.Platform.NewInjector != nil {
		injector = opts.Platform.NewInjector(cfg.Injection)
	}
	application := New(runtimeCtx, cfg, Dependencies{
		SourceFactory: func() (audio.Source, error) {
			if opts.Platform.NewSource == nil {
				return nil, audio.ErrUnavailable
			}
			return opts.Platform.NewSource(cfg.Audio)
		},
		ModelChecker:   r.modelChecker,
		ConfigReloader: runtimeConfigReloader(opts),
		Segmenter:      vad.NewSegmenter(cfg.VAD, cfg.Audio.SampleRate),
		Engine:         engine,
		ASRResolution:  resolution,
		ASRFallback: func() (asr.Engine, asr.Resolution, error) {
			fallback := cfg
			fallback.ASR.Engine = asr.EngineSherpa
			fallback.ASR.Provider = asr.ProviderCPU
			return resolveRuntimeASR(fallback, opts)
		},
		Injector: injector,
		Focus:    provider,
		Logger:   logger,
		Shutdown: func() {
			cancel()
			if opts.Shutdown != nil {
				opts.Shutdown()
			}
		},
	})
	r.App = application
	r.Server = control.NewServer(cfg.Daemon.Socket, application)
	if resolutionErr != nil {
		application.recordError(apiStateForStartup(opts), apperr.Code(resolutionErr), resolutionErr)
	}
	if focusErr != nil {
		focusErr = normalizeError(apperr.CodeFocusUnavailable, "check focus provider", focusErr)
		application.recordError(apiStateForStartup(opts), apperr.Code(focusErr), focusErr)
	}
	if resolutionErr == nil && cfg.Daemon.PreloadModel && engine != nil {
		if err := application.loadASR(runtimeCtx, 0); err != nil {
			err = normalizeError(apperr.CodeASRModelInvalid, "preload recognition model", err)
			if !opts.AllowDegradedStartup {
				_ = r.Close(context.Background())
				return nil, err
			}
			application.recordError(apiStateForStartup(opts), apperr.Code(err), err)
		}
	}
	r.logStartup()
	return r, nil
}

func (r *Runtime) Serve(ctx context.Context) error {
	if r == nil || r.Server == nil {
		return apperr.New(apperr.CodeInternalError, "serve runtime", fmt.Errorf("control server is unavailable"))
	}
	serveCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-r.ctx.Done():
			cancel()
		case <-serveCtx.Done():
		}
	}()
	err := r.Server.Serve(serveCtx)
	if err != nil {
		r.logger.Error("daemon stopped with error", "error", err)
	} else {
		r.logger.Info("daemon stopped")
	}
	return err
}

func (r *Runtime) Close(ctx context.Context) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	r.cancel()
	r.mu.Unlock()
	var errs []error
	if r.App != nil {
		status := r.App.Status(context.Background())
		if status.State != api.StateIdle {
			if err := r.App.Stop(ctx, false); err != nil {
				errs = append(errs, err)
			}
		}
		if r.App.guard != nil {
			r.App.guard.Reset()
		}
		r.App.mu.Lock()
		source := r.App.source
		r.App.source = nil
		r.App.mu.Unlock()
		if source != nil {
			if err := source.Stop(ctx); err != nil {
				errs = append(errs, err)
			}
			closeSource(source)
		}
		if err := r.App.CloseASR(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (r *Runtime) RestartASR(ctx context.Context) error {
	if r == nil || r.App == nil {
		return apperr.New(apperr.CodeASRBackendUnavailable, "restart ASR", fmt.Errorf("runtime is unavailable"))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return apperr.New(apperr.CodeASRBackendUnavailable, "restart ASR", fmt.Errorf("runtime is closed"))
	}
	if err := r.App.CloseASR(); err != nil {
		return normalizeError(apperr.CodeASRBackendUnavailable, "close ASR", err)
	}
	engine, resolution, err := resolveRuntimeASR(r.Config, r.opts)
	if err != nil {
		return err
	}
	r.App.setASREngine(engine, resolution)
	if err := r.App.loadASR(ctx, 0); err != nil {
		_ = r.App.CloseASR()
		return normalizeError(apperr.CodeASRModelInvalid, "restart ASR", err)
	}
	return nil
}

func (r *Runtime) RecreateAudio(ctx context.Context) error {
	if r == nil || r.App == nil {
		return audio.ErrUnavailable
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return audio.ErrUnavailable
	}
	return r.App.recreateSource(ctx)
}

func resolveRuntimeASR(cfg config.Config, opts RuntimeOptions) (asr.Engine, asr.Resolution, error) {
	provider := cfg.ASR.Provider
	if provider == "" && cfg.ASR.Engine == asr.EngineWhisper {
		provider = asr.ProviderVulkan
	}
	sherpaCfg := cfg.ASR
	sherpaCfg.Provider = asr.ProviderCPU
	deps := asr.ResolverDeps{
		NewSherpa: func() asr.Engine { return sherpaasr.New(sherpaCfg) },
		WhisperModelPath: func() (string, error) {
			path := cfg.WhisperModelPath()
			info, err := os.Stat(path)
			if err != nil {
				return "", apperr.New(apperr.CodeASRModelMissing, "locate whisper model", err)
			}
			if !info.Mode().IsRegular() {
				return "", apperr.New(apperr.CodeASRModelInvalid, "validate whisper model", fmt.Errorf("%s is not a regular file", path))
			}
			return path, nil
		},
	}
	if opts.ProbeAccelerator != nil {
		deps.ProbeGPU = func() (string, error) {
			accelerator, err := opts.ProbeAccelerator(asr.ProviderVulkan, cfg.ASR.GPUDevice)
			return accelerator.Name, err
		}
	}
	if opts.NewWhisper != nil {
		deps.NewWhisper = func(modelPath string, device int, useGPU bool) (asr.Engine, error) {
			selected := asr.ProviderCPU
			if useGPU {
				selected = asr.ProviderVulkan
			}
			engine, err := opts.NewWhisper(modelPath, selected, device, cfg.ASR.NumThreads)
			if err != nil {
				return nil, normalizeError(apperr.CodeASRBackendUnavailable, "construct whisper engine", err)
			}
			return engine, nil
		}
	}
	engine, resolution, err := asr.Resolve(cfg.ASR.Engine, provider, cfg.ASR.GPUDevice, deps)
	if err != nil {
		return nil, asr.Resolution{}, normalizeError(apperr.CodeASRBackendUnavailable, "resolve ASR", err)
	}
	return engine, resolution, nil
}

func runtimeModelChecker(cfg config.Config) func(string) error {
	return func(engine string) error {
		pinned := cfg
		if engine != "" {
			pinned.ASR.Engine = engine
			if engine == asr.EngineSherpa {
				pinned.ASR.Provider = asr.ProviderCPU
			}
		}
		result := model.CheckConfig(pinned, model.CheckOptions{StrictSizes: true})
		if !result.OK {
			return apperr.New(apperr.CodeASRModelInvalid, "validate recognition model", fmt.Errorf("%v", result.Errors))
		}
		return nil
	}
}

func runtimeConfigReloader(opts RuntimeOptions) func(context.Context) (config.Config, error) {
	return func(ctx context.Context) (config.Config, error) {
		select {
		case <-ctx.Done():
			return config.Config{}, ctx.Err()
		default:
		}
		cfg, err := config.Load(opts.ConfigPath)
		if err != nil {
			return config.Config{}, apperr.New(apperr.CodeConfigInvalid, "reload config", err)
		}
		if opts.LogLevelOverride != "" {
			cfg.Daemon.LogLevel = opts.LogLevelOverride
		}
		return cfg, nil
	}
}

func (r *Runtime) logStartup() {
	status := r.App.Status(context.Background()).ASR
	r.logger.Info("daemon starting",
		"platform", r.Platform.Name,
		"log_level", r.Config.Daemon.LogLevel,
		"socket", r.Config.Daemon.Socket,
		"audio_backend", r.Config.Audio.Backend,
		"audio_sample_rate", r.Config.Audio.SampleRate,
		"audio_quantum_ms", r.Config.Audio.QuantumMS,
		"vad_engine", r.Config.VAD.Engine,
		"asr_engine", r.Config.ASR.Engine,
		"asr_model_type", r.Config.ASR.ModelType,
		"asr_provider", r.Config.ASR.Provider,
		"asr_resolved_engine", status.ResolvedEngine,
		"asr_resolved_provider", status.ResolvedProvider,
		"asr_gpu_name", status.GPUName,
		"asr_fallback_reason", status.FallbackReason,
		"asr_threads", r.Config.ASR.NumThreads,
		"preload_model", r.Config.Daemon.PreloadModel,
		"redacted_transcripts", r.Config.Daemon.RedactTranscriptsInLogs)
}

func apiStateForStartup(opts RuntimeOptions) api.State {
	if opts.AllowDegradedStartup {
		return api.StateError
	}
	return api.StateIdle
}

type runtimeUnavailableFocus struct {
	name string
	err  error
}

func (p runtimeUnavailableFocus) Backend() string                 { return p.name }
func (p runtimeUnavailableFocus) Available(context.Context) error { return p.err }
func (p runtimeUnavailableFocus) Current(context.Context) (focus.Target, error) {
	return focus.Target{}, p.err
}
func (p runtimeUnavailableFocus) Same(context.Context, focus.Target) (focus.Target, bool, error) {
	return focus.Target{}, false, p.err
}
func (p runtimeUnavailableFocus) Release(focus.Target) {}
