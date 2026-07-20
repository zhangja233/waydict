package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"time"

	"waydict/internal/apperr"
	"waydict/internal/asr"
	remoteasr "waydict/internal/asr/remote"
	sherpaasr "waydict/internal/asr/sherpa"
	"waydict/internal/audio"
	"waydict/internal/config"
	"waydict/internal/control"
	"waydict/internal/focus"
	"waydict/internal/hotkey"
	"waydict/internal/inject"
	svlog "waydict/internal/log"
	"waydict/internal/loginitem"
	"waydict/internal/model"
	"waydict/internal/permissions"
	"waydict/internal/preferences"
	"waydict/internal/vad"
	"waydict/pkg/api"
)

type PlatformDependencies struct {
	Name             string
	Capabilities     ControlCapabilities
	NewSource        func(config.Audio) (audio.Source, error)
	NewInjector      func(config.Injection) inject.Injector
	NewFocusProvider func(config.Focus) focus.Provider
	PermissionSource permissions.Source
	DeviceManager    audio.DeviceManager
	Preferences      preferences.Store
	Hotkey           hotkey.Service
	LoginItem        loginitem.Service
	HostActions      HostActions
}

type WhisperFactory func(modelPath, provider string, device, threads int) (asr.Engine, error)

type RuntimeOptions struct {
	ConfigPath           string
	LogLevelOverride     string
	Platform             PlatformDependencies
	NewWhisper           WhisperFactory
	ProbeAccelerator     func(provider string, device int) (asr.Accelerator, error)
	AllowDegradedStartup bool
	LogOutput            io.Writer
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
	logLevel     *slog.LevelVar
	modelChecker func(string) error
	closed       bool
}

func NewRuntime(ctx context.Context, cfg config.Config, opts RuntimeOptions) (*Runtime, error) {
	cfg, preferenceWarning, err := applyRuntimePreferences(ctx, cfg, opts.Platform)
	if err != nil {
		return nil, err
	}
	if opts.LogLevelOverride != "" {
		cfg.Daemon.LogLevel = opts.LogLevelOverride
	}
	validationCapabilities := capabilitySetForRuntime(opts.Platform)
	if err := cfg.ValidateFor(validationCapabilities); err != nil {
		return nil, apperr.New(apperr.CodeConfigInvalid, "validate config", err)
	}
	if opts.Platform.Name == "" {
		opts.Platform.Name = "unknown"
	}
	runtimeCtx, cancel := context.WithCancel(ctx)
	logger, logLevel := svlog.NewDynamic(cfg.Daemon.LogLevel, opts.LogOutput)
	r := &Runtime{
		Config:   cfg,
		Platform: opts.Platform,
		ctx:      runtimeCtx,
		cancel:   cancel,
		opts:     opts,
		logger:   logger,
		logLevel: logLevel,
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
	focusRequired := cfg.Focus.Required || (cfg.Focus.Enabled && cfg.Focus.Backend != "auto" && cfg.Focus.Backend != "none")
	if cfg.Focus.Enabled || focusRequired {
		if opts.Platform.NewFocusProvider != nil {
			provider = opts.Platform.NewFocusProvider(cfg.Focus)
		}
		if provider == nil {
			focusErr = apperr.New(apperr.CodeFocusUnavailable, "construct focus provider", fmt.Errorf("focus provider factory is unavailable"))
		} else {
			focusCtx, focusCancel := context.WithTimeout(runtimeCtx, time.Second)
			focusErr = provider.Available(focusCtx)
			focusCancel()
		}
	}
	if focusErr != nil && focusRequired && !opts.AllowDegradedStartup {
		if engine != nil {
			_ = engine.Close()
		}
		_ = closeFocusProvider(provider)
		return fail(normalizeError(apperr.CodeFocusUnavailable, "check focus provider", focusErr))
	}
	if focusErr != nil && focusRequired && provider == nil {
		provider = runtimeUnavailableFocus{name: opts.Platform.Name, err: focusErr}
	} else if focusErr != nil && !focusRequired {
		_ = closeFocusProvider(provider)
		provider = nil
	}

	var injector inject.Injector
	if opts.Platform.NewInjector != nil {
		injector = opts.Platform.NewInjector(cfg.Injection)
	}
	statusCapabilities := opts.Platform.Capabilities
	if statusCapabilities.Platform == "" {
		statusCapabilities.Platform = opts.Platform.Name
	}
	if statusCapabilities.Host == "" {
		statusCapabilities.Host = "daemon"
	}
	application := New(runtimeCtx, cfg, Dependencies{
		AudioSourceFactory: func(audioCfg config.Audio) (audio.Source, error) {
			if opts.Platform.NewSource == nil {
				return nil, audio.ErrUnavailable
			}
			return opts.Platform.NewSource(audioCfg)
		},
		ModelChecker:   r.modelChecker,
		ConfigReloader: runtimeConfigReloader(opts),
		ConfigValidator: func(cfg config.Config) error {
			return cfg.ValidateFor(validationCapabilities)
		},
		Segmenter:     vad.NewSegmenter(cfg.VAD, cfg.Audio.SampleRate),
		Engine:        engine,
		ASRResolution: resolution,
		ASRFallback: func() (asr.Engine, asr.Resolution, error) {
			fallback := cfg
			fallback.ASR.Engine = asr.EngineSherpa
			fallback.ASR.Provider = asr.ProviderCPU
			return resolveRuntimeASR(fallback, opts)
		},
		Injector:         injector,
		Focus:            provider,
		Capabilities:     statusCapabilities,
		PermissionSource: opts.Platform.PermissionSource,
		DeviceManager:    opts.Platform.DeviceManager,
		Preferences:      opts.Platform.Preferences,
		Hotkey:           opts.Platform.Hotkey,
		LoginItem:        opts.Platform.LoginItem,
		HostActions:      opts.Platform.HostActions,
		EnsureASR:        r.ensureASR,
		SetLogLevel: func(level string) {
			svlog.SetLevel(logLevel, level)
		},
		Logger: logger,
		Shutdown: func() {
			cancel()
			if opts.Shutdown != nil {
				opts.Shutdown()
			}
		},
	})
	r.App = application
	r.App.hostActions.RestartRuntime = r.restartRuntime
	r.Server = control.NewServer(cfg.Daemon.Socket, application)
	if resolutionErr != nil {
		application.recordError(apiStateForStartup(opts), apperr.Code(resolutionErr), resolutionErr)
	}
	if focusErr != nil {
		focusErr = normalizeError(apperr.CodeFocusUnavailable, "check focus provider", focusErr)
		if focusRequired {
			application.recordError(apiStateForStartup(opts), apperr.Code(focusErr), focusErr)
		} else {
			application.recordWarning(apperr.CodeFocusUnavailable, focusErr.Error())
			application.mu.Lock()
			application.status.Focus.Backend = cfg.Focus.Backend
			application.status.Focus.Available = false
			application.status.Focus.DegradedReason = focusErr.Error()
			application.mu.Unlock()
		}
	}
	if preferenceWarning != "" {
		application.recordWarning(apperr.CodeConfigInvalid, preferenceWarning)
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
		if r.App.hotkey != nil {
			if err := r.App.hotkey.Stop(ctx); err != nil {
				errs = append(errs, err)
			}
		}
		if status.State != api.StateIdle {
			if err := r.App.Stop(ctx, false); err != nil {
				errs = append(errs, err)
			}
		}
		if r.App.guard != nil {
			r.App.guard.Reset()
		}
		r.App.audioRecreateMu.Lock()
		r.App.mu.Lock()
		source := r.App.source
		provider := r.App.focus
		r.App.source = nil
		r.App.focus = nil
		r.App.guard = nil
		r.App.sourceGeneration++
		r.App.mu.Unlock()
		if source != nil {
			if err := source.Stop(ctx); err != nil {
				errs = append(errs, err)
			}
			closeSource(source)
		}
		r.App.audioRecreateMu.Unlock()
		if err := closeFocusProvider(provider); err != nil {
			errs = append(errs, err)
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
	if err := r.App.requireIdle(); err != nil {
		return err
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
	segmenter := vad.NewSegmenter(r.Config.VAD, r.Config.Audio.SampleRate)
	r.App.segMu.Lock()
	r.App.mu.Lock()
	r.App.segmenter = segmenter
	r.App.status.VAD.Engine = segmenter.Name()
	r.App.mu.Unlock()
	r.App.segMu.Unlock()
	return nil
}

func (r *Runtime) ensureASR(ctx context.Context) error {
	if r == nil || r.App == nil {
		return apperr.New(apperr.CodeASRBackendUnavailable, "recreate ASR", fmt.Errorf("runtime is unavailable"))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return apperr.New(apperr.CodeASRBackendUnavailable, "recreate ASR", fmt.Errorf("runtime is closed"))
	}
	r.App.mu.Lock()
	present := r.App.engine != nil
	r.App.mu.Unlock()
	if present {
		return nil
	}
	engine, resolution, err := resolveRuntimeASR(r.Config, r.opts)
	if err != nil {
		return err
	}
	r.App.setASREngine(engine, resolution)
	return nil
}

func (r *Runtime) Suspend(ctx context.Context) error {
	if r == nil || r.App == nil {
		return audio.ErrUnavailable
	}
	var errs []error
	status := r.App.Status(ctx)
	if status.State != api.StateIdle && status.State != api.StateError {
		if err := r.App.Stop(ctx, false); err != nil {
			errs = append(errs, err)
		}
	}
	r.App.audioRecreateMu.Lock()
	r.App.mu.Lock()
	source := r.App.source
	r.App.source = nil
	r.App.sourceGeneration++
	r.App.status.Audio.Capturing = false
	r.App.mu.Unlock()
	if source != nil {
		if err := source.Stop(ctx); err != nil {
			errs = append(errs, err)
		}
		closeSource(source)
	}
	r.App.audioRecreateMu.Unlock()
	if r.App.guard != nil {
		r.App.guard.Reset()
	}
	r.App.segMu.Lock()
	if r.App.segmenter != nil {
		r.App.segmenter.Reset()
	}
	r.App.segMu.Unlock()
	return errors.Join(errs...)
}

func (r *Runtime) ReleaseASRForMemoryPressure() (bool, error) {
	if r == nil || r.App == nil {
		return false, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return false, nil
	}
	engine, detached := r.App.detachASRIfIdle()
	if !detached {
		return false, nil
	}
	if err := engine.Close(); err != nil {
		return false, normalizeError(apperr.CodeASRBackendUnavailable, "release ASR after memory pressure", err)
	}
	return true, nil
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
	platform := cfg.Platform()
	if opts.Platform.Name != "" {
		platform = opts.Platform.Name
	}
	preferred := config.PreferredWhisperProviderFor(platform)
	sherpaCfg := cfg.ASR
	sherpaCfg.Provider = asr.ProviderCPU
	deps := asr.ResolverDeps{
		PreferredWhisperProvider: preferred,
		NumThreads:               cfg.ASR.NumThreads,
		NewSherpa:                func() asr.Engine { return sherpaasr.New(sherpaCfg) },
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
		RemoteFallback: cfg.ASR.Remote.Fallback,
		NewRemote: func(fallback asr.Engine) (asr.Engine, error) {
			return remoteasr.New(remoteasr.OptionsFromConfig(cfg), fallback), nil
		},
	}
	if opts.ProbeAccelerator != nil {
		deps.ProbeAccelerator = opts.ProbeAccelerator
	}
	if opts.NewWhisper != nil {
		deps.NewWhisper = func(modelPath, selected string, device, threads int) (asr.Engine, error) {
			engine, err := opts.NewWhisper(modelPath, selected, device, threads)
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
		cfg, _, err = applyRuntimePreferences(ctx, cfg, opts.Platform)
		if err != nil {
			return config.Config{}, err
		}
		return cfg, nil
	}
}

func applyRuntimePreferences(ctx context.Context, cfg config.Config, platform PlatformDependencies) (config.Config, string, error) {
	if platform.Preferences == nil {
		return cfg, "", nil
	}
	var validDevice func(string) bool
	if platform.DeviceManager != nil {
		if devices, err := platform.DeviceManager.Devices(ctx); err == nil {
			valid := make(map[string]bool, len(devices))
			for _, device := range devices {
				valid[device.ID] = device.Connected
			}
			validDevice = func(id string) bool { return valid[id] }
		}
	}
	device, audioWarning, err := preferences.AudioDevice(ctx, cfg, platform.Preferences, validDevice)
	if err != nil {
		return config.Config{}, "", apperr.New(apperr.CodeConfigInvalid, "load audio preference", err)
	}
	cfg.Audio.Device = device.Value
	mode, hotkeyWarning, err := preferences.HotkeyMode(ctx, cfg, platform.Preferences)
	if err != nil {
		return config.Config{}, "", apperr.New(apperr.CodeConfigInvalid, "load hotkey preference", err)
	}
	cfg.Hotkey.Mode = mode.Value
	warning := audioWarning
	if warning == "" {
		warning = hotkeyWarning
	} else if hotkeyWarning != "" {
		warning += "; " + hotkeyWarning
	}
	return cfg, warning, nil
}

func capabilitySetForRuntime(platform PlatformDependencies) config.CapabilitySet {
	caps := platform.Capabilities
	if caps.Platform == "" && caps.AudioBackends == nil && caps.InjectionEngines == nil && caps.FocusBackends == nil && caps.WhisperProviders == nil && caps.SherpaProviders == nil {
		if platform.Name == "linux" || platform.Name == "darwin" {
			return config.CapabilitySetFor(platform.Name)
		}
		return config.CurrentCapabilitySet()
	}
	name := caps.Platform
	if name == "" {
		name = platform.Name
	}
	return config.CapabilitySet{
		Platform:         name,
		AudioBackends:    append([]string(nil), caps.AudioBackends...),
		InjectionEngines: append([]string(nil), caps.InjectionEngines...),
		FocusBackends:    append([]string(nil), caps.FocusBackends...),
		WhisperProviders: append([]string(nil), caps.WhisperProviders...),
		SherpaProviders:  append([]string(nil), caps.SherpaProviders...),
		HotkeyAllowed:    name == "darwin",
	}
}

func (r *Runtime) restartRuntime(ctx context.Context, next config.Config) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.App == nil {
		return apperr.New(apperr.CodeInternalError, "restart runtime", fmt.Errorf("runtime is unavailable"))
	}
	if err := next.ValidateFor(capabilitySetForRuntime(r.Platform)); err != nil {
		return apperr.New(apperr.CodeConfigInvalid, "restart runtime", err)
	}
	r.App.audioRecreateMu.Lock()
	defer r.App.audioRecreateMu.Unlock()
	r.App.mu.Lock()
	busy := r.App.capturing || r.App.pendingASR > 0 || len(r.App.deferred) > 0
	r.App.mu.Unlock()
	if busy {
		return withCode("busy", fmt.Errorf("cannot restart while dictation is active"))
	}
	engine, resolution, err := resolveRuntimeASR(next, r.opts)
	if err != nil {
		return err
	}
	checker := runtimeModelChecker(next)
	if resolution.Engine == asr.EngineSherpa {
		if err := validateResolvedSherpaConfig(next); err != nil {
			_ = engine.Close()
			return err
		}
		if next.Daemon.PreloadModel {
			if err := checker(resolution.Engine); err != nil {
				_ = engine.Close()
				return err
			}
		}
	}
	if next.Daemon.PreloadModel && engine != nil {
		if err := engine.Load(ctx); err != nil {
			_ = engine.Close()
			return normalizeError(apperr.CodeASRModelInvalid, "restart runtime", err)
		}
	}
	var provider focus.Provider
	var focusErr error
	focusRequired := next.Focus.Required || (next.Focus.Enabled && next.Focus.Backend != "auto" && next.Focus.Backend != "none")
	if next.Focus.Enabled && r.Platform.NewFocusProvider != nil {
		provider = r.Platform.NewFocusProvider(next.Focus)
	}
	if next.Focus.Enabled {
		if provider == nil {
			focusErr = apperr.New(apperr.CodeFocusUnavailable, "restart runtime", fmt.Errorf("focus provider is unavailable"))
		} else {
			focusCtx, cancel := context.WithTimeout(ctx, time.Second)
			focusErr = provider.Available(focusCtx)
			cancel()
		}
	}
	if focusErr != nil && focusRequired {
		_ = engine.Close()
		_ = closeFocusProvider(provider)
		return normalizeError(apperr.CodeFocusUnavailable, "restart runtime", focusErr)
	}
	if focusErr != nil {
		_ = closeFocusProvider(provider)
		provider = nil
	}
	var injector inject.Injector
	if r.Platform.NewInjector != nil {
		injector = r.Platform.NewInjector(next.Injection)
	}
	segmenter := vad.NewSegmenter(next.VAD, next.Audio.SampleRate)

	r.App.mu.Lock()
	oldEngine := r.App.engine
	oldSource := r.App.source
	oldProvider := r.App.focus
	r.App.cfg = next
	r.App.engine = engine
	r.App.asrResolution = resolution
	r.App.modelChecker = checker
	r.App.asrFallback = func() (asr.Engine, asr.Resolution, error) {
		fallback := next
		fallback.ASR.Engine = asr.EngineSherpa
		fallback.ASR.Provider = asr.ProviderCPU
		return resolveRuntimeASR(fallback, r.opts)
	}
	r.App.segmenter = segmenter
	r.App.injector = injector
	r.App.focus = provider
	r.App.source = nil
	r.App.sourceGeneration++
	if provider != nil {
		r.App.guard = focus.NewGuard(provider, focus.Policy(next.EffectiveFocusPolicy()))
	} else {
		r.App.guard = nil
	}
	r.App.post = inject.NewPostProcessor(next.PostProcess, next.Injection.AppendSpace)
	r.App.status.Audio.Backend = next.Audio.Backend
	r.App.status.Audio.SampleRate = next.Audio.SampleRate
	r.App.status.Audio.DeviceID = next.Audio.Device
	r.App.status.Audio.Capturing = false
	r.App.status.VAD.Engine = segmenter.Name()
	r.App.status.ASR = api.ASRStatus{
		Engine:           next.ASR.Engine,
		Model:            resolvedModelName(next, resolution),
		Provider:         next.ASR.Provider,
		ResolvedEngine:   resolution.Engine,
		ResolvedProvider: resolution.Provider,
		GPUName:          resolution.GPUName,
		FallbackReason:   resolution.FallbackReason,
		NumThreads:       next.ASR.NumThreads,
		Loaded:           engine != nil && engine.Loaded(),
	}
	r.App.status.Injection.Engine = injectorBackend(injector, next.Injection.Engine)
	r.App.status.Focus = api.FocusStatus{Backend: focusBackend(provider), Available: provider != nil, Sway: provider != nil && provider.Backend() == "sway"}
	if focusErr != nil {
		r.App.status.Focus.Backend = next.Focus.Backend
		r.App.status.Focus.DegradedReason = focusErr.Error()
	}
	r.App.mu.Unlock()

	r.Config = next
	r.modelChecker = checker
	svlog.SetLevel(r.logLevel, next.Daemon.LogLevel)
	closeSource(oldSource)
	if oldEngine != nil {
		_ = oldEngine.Close()
	}
	_ = closeFocusProvider(oldProvider)
	return nil
}

func closeFocusProvider(provider focus.Provider) error {
	closer, ok := provider.(interface{ Close() error })
	if !ok {
		return nil
	}
	return closer.Close()
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
