package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"time"

	"waydict/internal/apperr"
	"waydict/internal/asr"
	"waydict/internal/audio"
	"waydict/internal/config"
	"waydict/internal/control"
	"waydict/internal/focus"
	"waydict/internal/hotkey"
	"waydict/internal/inject"
	"waydict/internal/loginitem"
	"waydict/internal/permissions"
	"waydict/internal/preferences"
	"waydict/internal/vad"
	"waydict/pkg/api"
)

type Dependencies struct {
	Source             audio.Source
	SourceFactory      func() (audio.Source, error)
	AudioSourceFactory func(config.Audio) (audio.Source, error)
	// ModelChecker validates model files for the given resolved engine.
	ModelChecker     func(engine string) error
	ConfigReloader   func(context.Context) (config.Config, error)
	ConfigValidator  func(config.Config) error
	Segmenter        vad.Segmenter
	Engine           asr.Engine
	ASRResolution    asr.Resolution
	ASRFallback      func() (asr.Engine, asr.Resolution, error)
	Injector         inject.Injector
	Focus            focus.Provider
	Capabilities     ControlCapabilities
	PermissionSource permissions.Source
	DeviceManager    audio.DeviceManager
	Preferences      preferences.Store
	Hotkey           hotkey.Service
	LoginItem        loginitem.Service
	HostActions      HostActions
	SetLogLevel      func(string)
	Logger           *slog.Logger
	Shutdown         func()
}

type ControlCapabilities struct {
	Platform         string
	Host             string
	AudioBackends    []string
	InjectionEngines []string
	FocusBackends    []string
	WhisperProviders []string
	SherpaProviders  []string
	HotkeyAvailable  bool
}

type HostActions struct {
	Activate              func(context.Context) error
	RestartRuntime        func(context.Context, config.Config) error
	InstallRequiredModels func(context.Context) error
	RevealModels          func(context.Context) error
	OpenConfig            func(context.Context) error
	OpenLog               func(context.Context) error
	RunDiagnostics        func(context.Context) (map[string]any, error)
	CopyDiagnostics       func(context.Context) error
}

type App struct {
	cfg                config.Config
	source             audio.Source
	sourceFactory      func() (audio.Source, error)
	audioSourceFactory func(config.Audio) (audio.Source, error)
	modelChecker       func(engine string) error
	configReloader     func(context.Context) (config.Config, error)
	configValidator    func(config.Config) error
	segmenter          vad.Segmenter
	engine             asr.Engine
	asrResolution      asr.Resolution
	asrFallback        func() (asr.Engine, asr.Resolution, error)
	injector           inject.Injector
	guard              *focus.Guard
	focus              focus.Provider
	capabilities       ControlCapabilities
	permissionSource   permissions.Source
	deviceManager      audio.DeviceManager
	preferences        preferences.Store
	hotkey             hotkey.Service
	loginItem          loginitem.Service
	hostActions        HostActions
	setLogLevel        func(string)
	logger             *slog.Logger
	post               inject.PostProcessor
	caseState          inject.CaseState

	mu             sync.Mutex
	segMu          sync.Mutex // serializes segmenter access (silero VAD is not concurrency-safe)
	status         api.Status
	startedAt      time.Time
	sessionCancel  context.CancelFunc
	captureDone    chan struct{}
	capturing      bool
	rootCtx        context.Context
	asrQueue       chan segmentJob
	shutdown       func()
	lastActivity   time.Time
	retryingAudio  bool
	currentSession uint64
	discarded      map[uint64]struct{}
	pendingASR     int
	pendingSession map[uint64]int
	deferred       map[uint64]*deferredSession
	segmentOpen    bool
	pendingConfig  *config.Config
}

type segmentJob struct {
	session uint64
	segment asr.AudioSegment
}

func New(ctx context.Context, cfg config.Config, deps Dependencies) *App {
	resolution := deps.ASRResolution
	if resolution.Engine == "" && deps.Engine != nil {
		resolution.Engine = deps.Engine.Name()
		resolution.Provider = cfg.ASR.Provider
	}
	a := &App{
		cfg:                cfg,
		source:             deps.Source,
		sourceFactory:      deps.SourceFactory,
		audioSourceFactory: deps.AudioSourceFactory,
		modelChecker:       deps.ModelChecker,
		configReloader:     deps.ConfigReloader,
		configValidator:    deps.ConfigValidator,
		segmenter:          deps.Segmenter,
		engine:             deps.Engine,
		asrResolution:      resolution,
		asrFallback:        deps.ASRFallback,
		injector:           deps.Injector,
		focus:              deps.Focus,
		capabilities:       deps.Capabilities,
		permissionSource:   deps.PermissionSource,
		deviceManager:      deps.DeviceManager,
		preferences:        deps.Preferences,
		hotkey:             deps.Hotkey,
		loginItem:          deps.LoginItem,
		hostActions:        deps.HostActions,
		setLogLevel:        deps.SetLogLevel,
		logger:             deps.Logger,
		post:               inject.NewPostProcessor(cfg.PostProcess, cfg.Injection.AppendSpace),
		caseState:          inject.CaseState{AtBoundary: true},
		startedAt:          time.Now(),
		rootCtx:            ctx,
		asrQueue:           make(chan segmentJob, 8),
		shutdown:           deps.Shutdown,
		discarded:          make(map[uint64]struct{}),
		pendingSession:     make(map[uint64]int),
		deferred:           make(map[uint64]*deferredSession),
	}
	if deps.Focus != nil {
		a.guard = focus.NewGuard(deps.Focus, focus.Policy(cfg.EffectiveFocusPolicy()))
	}
	vadEngine := ""
	if deps.Segmenter != nil {
		vadEngine = deps.Segmenter.Name()
	}
	a.status = api.Status{
		State: api.StateIdle,
		Audio: api.AudioStatus{
			Backend:    cfg.Audio.Backend,
			SampleRate: cfg.Audio.SampleRate,
			LevelDBFS:  -120,
		},
		VAD: api.VADStatus{Engine: vadEngine},
		ASR: api.ASRStatus{
			Engine:           cfg.ASR.Engine,
			Model:            resolvedModelName(cfg, resolution),
			Provider:         cfg.ASR.Provider,
			ResolvedEngine:   resolution.Engine,
			ResolvedProvider: resolution.Provider,
			FallbackReason:   resolution.FallbackReason,
			NumThreads:       cfg.ASR.NumThreads,
			Loaded:           deps.Engine != nil && deps.Engine.Loaded(),
		},
		Injection: api.InjectionStatus{
			Engine: injectorBackend(deps.Injector, cfg.Injection.Engine),
		},
		Focus: api.FocusStatus{
			Backend:   focusBackend(deps.Focus),
			Available: deps.Focus != nil,
			Sway:      deps.Focus != nil && deps.Focus.Backend() == "sway",
		},
		LastTranscriptRedacted: cfg.Daemon.RedactTranscriptsInLogs,
	}
	if deps.Capabilities.Platform != "" || deps.Capabilities.Host != "" {
		a.status.Platform = &api.PlatformStatus{
			OS:                deps.Capabilities.Platform,
			Host:              deps.Capabilities.Host,
			Arch:              runtime.GOARCH,
			ConfigPath:        cfg.ActivePath(),
			LegacyConfig:      cfg.LegacyPathActive(),
			MigrationWarnings: cfg.MigrationWarnings(),
		}
	}
	if deps.Hotkey != nil || cfg.Hotkey.Enabled {
		a.status.Hotkey = &api.HotkeyStatus{
			Enabled:   cfg.Hotkey.Enabled,
			Available: deps.Hotkey != nil,
			Key:       cfg.Hotkey.Key,
			Modifiers: append([]string(nil), cfg.Hotkey.Modifiers...),
			Mode:      api.Mode(cfg.Hotkey.Mode),
		}
	}
	go a.asrWorker(ctx)
	return a
}

func (a *App) HandleControl(ctx context.Context, req control.Request) control.Response {
	if req.Command != "status" && req.Command != "inject_text" {
		a.logDebug("control request", "command", req.Command, "request_id", req.ID)
	}
	switch req.Command {
	case "start":
		mode, ok := parseMode(stringArg(req.Args, "mode"))
		if !ok {
			return control.Fail(req.ID, "usage", "mode must be toggle, oneshot, or hold", a.Status(ctx))
		}
		if err := a.Start(ctx, mode); err != nil {
			code := codeFor(err)
			return control.Fail(req.ID, code, err.Error(), a.Status(ctx))
		}
		return control.OK(req.ID, a.Status(ctx))
	case "stop":
		commit := boolArg(req.Args, "commit")
		discard := boolArg(req.Args, "discard")
		if commit && discard {
			return control.Fail(req.ID, "usage", "stop accepts only one of commit or discard", a.Status(ctx))
		}
		if !commit && !discard {
			commit = true
		}
		if err := a.Stop(ctx, commit); err != nil {
			code := codeFor(err)
			return control.Fail(req.ID, code, err.Error(), a.Status(ctx))
		}
		return control.OK(req.ID, a.Status(ctx))
	case "release":
		if err := a.Release(ctx); err != nil {
			code := codeFor(err)
			return control.Fail(req.ID, code, err.Error(), a.Status(ctx))
		}
		return control.OK(req.ID, a.Status(ctx))
	case "toggle":
		if err := a.Toggle(ctx); err != nil {
			code := codeFor(err)
			return control.Fail(req.ID, code, err.Error(), a.Status(ctx))
		}
		return control.OK(req.ID, a.Status(ctx))
	case "status":
		return control.OK(req.ID, a.Status(ctx))
	case "reload_config":
		if err := a.ReloadConfig(ctx); err != nil {
			code := codeFor(err)
			return control.Fail(req.ID, code, err.Error(), a.Status(ctx))
		}
		return control.OK(req.ID, a.Status(ctx))
	case "shutdown":
		_ = a.Stop(ctx, false)
		if a.shutdown != nil {
			go func() {
				time.Sleep(50 * time.Millisecond)
				a.shutdown()
			}()
		}
		return control.OK(req.ID, a.Status(ctx))
	default:
		if response, handled := a.handleExtendedControl(ctx, req); handled {
			return response
		}
		return control.Fail(req.ID, "usage", "unsupported command", a.Status(ctx))
	}
}

type StartOrigin string

const (
	StartOriginControl StartOrigin = "control"
	StartOriginHotkey  StartOrigin = "hotkey"
	StartOriginMenu    StartOrigin = "menu"
	StartOriginTest    StartOrigin = "onboarding_test"
)

type StartOptions struct {
	Mode             api.Mode
	Origin           StartOrigin
	ExpectedFocusPID int
}

func (a *App) Start(ctx context.Context, mode api.Mode) error {
	return a.StartWithOptions(ctx, StartOptions{Mode: mode, Origin: StartOriginControl})
}

func (a *App) StartWithOptions(ctx context.Context, opts StartOptions) error {
	if opts.Origin == "" {
		opts.Origin = StartOriginControl
	}
	a.mu.Lock()
	if a.status.State != api.StateIdle && a.status.State != api.StateError {
		state := a.status.State
		a.mu.Unlock()
		a.logDebug("start ignored", "mode", opts.Mode, "origin", opts.Origin, "state", state)
		return nil
	}
	a.status.State = api.StateArming
	a.status.Mode = modePtr(opts.Mode)
	a.status.LastError = nil
	a.status.LastWarning = nil
	a.caseState = inject.CaseState{AtBoundary: true}
	a.currentSession++
	session := a.currentSession
	a.mu.Unlock()
	a.logInfo("start requested", "session", session, "mode", opts.Mode, "origin", opts.Origin)

	if a.guard != nil && a.cfg.Focus.Enabled {
		a.guard.Reset()
		if err := a.guard.CaptureStart(ctx, opts.ExpectedFocusPID); err != nil {
			return a.failStart(apperr.CodeFocusUnavailable, "capture session focus", err)
		}
		a.recordFocus(a.guard.StartedMetadata())
	}

	if a.injector != nil {
		a.logDebug("checking injector", "session", session)
		if err := a.injector.Available(ctx); err != nil {
			return a.failStart(apperr.CodeInjectorUnavailable, "check injector", err)
		}
	}
	if a.engine != nil && !a.engine.Loaded() {
		if err := a.loadASR(ctx, session); err != nil {
			return a.failStart(apperr.CodeASRModelInvalid, "load recognition model", err)
		}
	}
	a.mu.Lock()
	src := a.source
	a.mu.Unlock()
	if src == nil {
		a.logInfo("audio source recreate start", "session", session)
		if err := a.recreateSource(ctx); err != nil {
			return a.failStart(apperr.CodeAudioBackendUnavailable, "create audio source", err)
		}
		a.logInfo("audio source recreate complete", "session", session)
	}
	a.mu.Lock()
	src = a.source
	a.mu.Unlock()
	if src == nil {
		return a.failStart(apperr.CodeAudioBackendUnavailable, "create audio source", audio.ErrUnavailable)
	}
	a.logDebug("audio source start", "session", session)
	if err := src.Start(ctx); err != nil {
		firstErr := err
		a.clearSource(src)
		if retryErr := a.recreateSource(ctx); retryErr != nil {
			err = fmt.Errorf("restart capture after %v: %w", firstErr, retryErr)
			return a.failStart(apperr.CodeAudioStartFailed, "start audio capture", err)
		}
		a.mu.Lock()
		src = a.source
		a.mu.Unlock()
		if src == nil {
			err = audio.ErrUnavailable
		} else {
			err = src.Start(ctx)
		}
		if err != nil {
			a.clearSource(src)
			return a.failStart(apperr.CodeAudioStartFailed, "start audio capture", err)
		}
	}
	sessionCtx, cancel := context.WithCancel(a.rootCtx)
	captureDone := make(chan struct{})
	a.mu.Lock()
	a.sessionCancel = cancel
	a.captureDone = captureDone
	a.capturing = true
	a.segmentOpen = false
	a.lastActivity = time.Now()
	a.status.State = api.StateListening
	a.status.Audio.Capturing = true
	a.status.ASR.Loaded = a.engine != nil && a.engine.Loaded()
	if deferredMode(opts.Mode) {
		a.deferred[session] = &deferredSession{
			mode:      opts.Mode,
			caseState: inject.CaseState{AtBoundary: true},
		}
	}
	a.mu.Unlock()
	a.logInfo("capture started", "session", session)
	go func() {
		defer close(captureDone)
		a.captureLoop(sessionCtx)
	}()
	go a.autoStopLoop(sessionCtx)
	return nil
}

func (a *App) failStart(code, operation string, err error) error {
	err = normalizeError(code, operation, err)
	if a.guard != nil {
		a.guard.Reset()
	}
	a.recordError(api.StateIdle, apperr.Code(err), err)
	return err
}

func (a *App) Stop(ctx context.Context, commit bool) error {
	return a.stop(ctx, commit, nil)
}

func (a *App) Release(ctx context.Context) error {
	mode := api.ModeHold
	return a.stop(ctx, true, &mode)
}

func (a *App) stop(ctx context.Context, commit bool, expectedMode *api.Mode) error {
	a.mu.Lock()
	if expectedMode != nil && (a.status.Mode == nil || *a.status.Mode != *expectedMode) {
		active := a.status.Mode
		a.mu.Unlock()
		a.logDebug("release ignored", "expected_mode", *expectedMode, "active_mode", active)
		return nil
	}
	cancel := a.sessionCancel
	captureDone := a.captureDone
	session := a.currentSession
	a.sessionCancel = nil
	a.captureDone = nil
	wasCapturing := a.capturing
	a.capturing = false
	a.segmentOpen = false
	a.status.Audio.Capturing = false
	if !commit {
		a.discarded[session] = struct{}{}
		if deferred := a.deferred[session]; deferred != nil {
			deferred.parts = nil
		}
	}
	pending := a.pendingASR
	a.mu.Unlock()
	a.logInfo("stop requested", "session", session, "commit", commit, "was_capturing", wasCapturing, "pending_asr", pending)
	if cancel != nil {
		cancel()
	}
	a.mu.Lock()
	src := a.source
	a.mu.Unlock()
	if src != nil && wasCapturing {
		a.logDebug("audio source pause", "session", session)
		if err := src.Pause(ctx); err != nil {
			err = normalizeError(apperr.CodeAudioStartFailed, "pause audio capture", err)
			if a.guard != nil {
				a.guard.Reset()
			}
			a.recordError(api.StateIdle, apperr.Code(err), err)
			return err
		}
	}
	// Wait for captureLoop to finish before flushing: the segmenter is not safe
	// for concurrent use, and a racing Feed vs Flush corrupts the silero VAD's
	// C heap (SIGABRT). This also guarantees no stale loop survives into a
	// subsequent Start.
	if captureDone != nil {
		<-captureDone
	}
	a.logDebug("capture loop joined", "session", session)
	if a.segmenter != nil {
		a.segMu.Lock()
		flushed := a.segmenter.Flush(commit, time.Now())
		a.segMu.Unlock()
		a.logDebug("segmenter flushed", "session", session, "commit", commit, "segments", len(flushed))
		for _, seg := range flushed {
			a.queueSegment(seg)
		}
	}
	a.mu.Lock()
	finalize := false
	resetFocus := !commit
	if commit {
		if deferred := a.deferred[session]; deferred != nil {
			deferred.commitRequested = true
			finalize = a.beginDeferredFinalizeLocked(session)
		} else if a.pendingSession[session] == 0 {
			a.status.State = api.StateIdle
			a.status.Mode = nil
			resetFocus = true
		} else {
			a.status.State = api.StateRecognizing
		}
	} else {
		if a.pendingSession[session] == 0 {
			deferred := a.deferred[session]
			if deferred == nil || !deferred.finalizing {
				delete(a.discarded, session)
				delete(a.deferred, session)
			}
		}
		a.status.State = api.StateIdle
		a.status.Mode = nil
	}
	a.mu.Unlock()
	if resetFocus && a.guard != nil {
		a.guard.Reset()
	}
	if finalize {
		go a.finalizeDeferred(session)
	}
	return nil
}

func (a *App) ReloadConfig(ctx context.Context) error {
	if a.configReloader == nil {
		return withCode("reload_unavailable", fmt.Errorf("config reload is unavailable"))
	}
	a.mu.Lock()
	if a.capturing || a.pendingASR > 0 || len(a.deferred) > 0 {
		a.mu.Unlock()
		return withCode("busy", fmt.Errorf("cannot reload config while dictation is active"))
	}
	a.mu.Unlock()

	cfg, err := a.configReloader(ctx)
	if err != nil {
		return withCode("config_invalid", err)
	}
	validate := a.configValidator
	if validate == nil {
		validate = func(cfg config.Config) error { return cfg.Validate() }
	}
	if err := validate(cfg); err != nil {
		return withCode("config_invalid", err)
	}

	a.mu.Lock()
	if a.capturing || a.pendingASR > 0 || len(a.deferred) > 0 {
		a.mu.Unlock()
		return withCode("busy", fmt.Errorf("cannot reload config while dictation is active"))
	}
	old := a.cfg
	restartErr := reloadRequiresRestart(old, cfg)
	active := cfg
	if restartErr != nil {
		active = liveConfig(old, cfg)
		pending := cfg
		a.pendingConfig = &pending
		a.status.PendingRestart = true
		a.status.LastWarning = &api.ErrorInfo{Code: "restart_required", Message: restartErr.Error()}
	} else {
		a.pendingConfig = nil
		a.status.PendingRestart = false
		if a.status.LastWarning != nil && a.status.LastWarning.Code == "restart_required" {
			a.status.LastWarning = nil
		}
	}
	a.cfg = active
	a.post = inject.NewPostProcessor(active.PostProcess, active.Injection.AppendSpace)
	if a.focus != nil {
		a.guard = focus.NewGuard(a.focus, focus.Policy(active.EffectiveFocusPolicy()))
	}
	a.status.LastTranscriptRedacted = active.Daemon.RedactTranscriptsInLogs
	if active.Daemon.RedactTranscriptsInLogs {
		a.status.LastTranscript = ""
		a.status.LastUninjectedText = ""
	}
	if a.status.Platform != nil {
		a.status.Platform.ConfigPath = cfg.ActivePath()
		a.status.Platform.LegacyConfig = cfg.LegacyPathActive()
		a.status.Platform.MigrationWarnings = cfg.MigrationWarnings()
	}
	if a.status.Hotkey != nil {
		a.status.Hotkey.Enabled = active.Hotkey.Enabled
		a.status.Hotkey.Key = active.Hotkey.Key
		a.status.Hotkey.Modifiers = append([]string(nil), active.Hotkey.Modifiers...)
		a.status.Hotkey.Mode = api.Mode(active.Hotkey.Mode)
	}
	audioChanged := old.Audio.Device != active.Audio.Device
	hotkeyChanged := !reflect.DeepEqual(old.Hotkey, active.Hotkey)
	logLevelChanged := old.Daemon.LogLevel != active.Daemon.LogLevel
	a.mu.Unlock()
	if logLevelChanged && a.setLogLevel != nil {
		a.setLogLevel(active.Daemon.LogLevel)
	}
	if audioChanged && (a.audioSourceFactory != nil || a.sourceFactory != nil) {
		if err := a.recreateSource(ctx); err != nil {
			return err
		}
	}
	if hotkeyChanged && active.Hotkey.Enabled && a.hotkey != nil {
		binding, err := hotkeyBinding(active.Hotkey)
		if err != nil {
			return err
		}
		if err := a.hotkey.Rebind(ctx, binding); err != nil {
			return err
		}
	}
	a.logInfo("config reloaded",
		"log_level", active.Daemon.LogLevel,
		"redacted_transcripts", active.Daemon.RedactTranscriptsInLogs,
		"auto_stop_seconds", active.Daemon.AutoStopAfterSilenceSeconds,
		"append_space", active.Injection.AppendSpace,
		"debug_save_audio", active.Debug.SaveAudioSegments,
		"pending_restart", restartErr != nil)
	return nil
}

func reloadRequiresRestart(old, next config.Config) error {
	oldDaemon := old.Daemon
	nextDaemon := next.Daemon
	oldDaemon.AutoStopAfterSilenceSeconds = nextDaemon.AutoStopAfterSilenceSeconds
	oldDaemon.RedactTranscriptsInLogs = nextDaemon.RedactTranscriptsInLogs
	oldDaemon.LogLevel = nextDaemon.LogLevel
	if oldDaemon != nextDaemon {
		return fmt.Errorf("daemon socket or preload changes require restart")
	}
	if old.Audio != next.Audio {
		oldAudio := old.Audio
		nextAudio := next.Audio
		oldAudio.Device = nextAudio.Device
		oldAudio.TargetObject = nextAudio.TargetObject
		if oldAudio != nextAudio {
			return fmt.Errorf("audio backend, format, or ring changes require restart")
		}
	}
	if old.VAD != next.VAD {
		return fmt.Errorf("VAD changes require restart")
	}
	// Resolution is startup-only; swapping engines would race the ASR worker.
	if !reflect.DeepEqual(old.ASR, next.ASR) {
		return fmt.Errorf("ASR changes require restart")
	}
	oldInjection := old.Injection
	nextInjection := next.Injection
	oldInjection.AppendSpace = nextInjection.AppendSpace
	oldInjection.KeyDelayMS = nextInjection.KeyDelayMS
	oldInjection.TimeoutMS = nextInjection.TimeoutMS
	oldInjection.FocusPolicy = nextInjection.FocusPolicy
	if oldInjection != nextInjection {
		return fmt.Errorf("injection engine changes require restart")
	}
	oldFocus := old.Focus
	nextFocus := next.Focus
	oldFocus.Enabled = nextFocus.Enabled
	oldFocus.Policy = nextFocus.Policy
	if oldFocus != nextFocus {
		return fmt.Errorf("focus backend changes require restart")
	}
	if old.Sway != next.Sway {
		return fmt.Errorf("Sway adapter changes require restart")
	}
	return nil
}

func liveConfig(old, next config.Config) config.Config {
	active := next
	active.Daemon.Socket = old.Daemon.Socket
	active.Daemon.PreloadModel = old.Daemon.PreloadModel
	active.Audio = old.Audio
	active.Audio.Device = next.Audio.Device
	active.Audio.TargetObject = next.Audio.TargetObject
	active.VAD = old.VAD
	active.ASR = old.ASR
	active.Injection.Engine = old.Injection.Engine
	active.Injection.Method = old.Injection.Method
	active.Injection.WtypePath = old.Injection.WtypePath
	active.Focus.Backend = old.Focus.Backend
	active.Focus.Required = old.Focus.Required
	active.Focus.Socket = old.Focus.Socket
	active.Sway = old.Sway
	return active
}

func (a *App) loadASR(ctx context.Context, session uint64) error {
	engine := a.engine
	resolution := a.asrResolution
	if resolution.Engine == asr.EngineSherpa {
		if err := validateResolvedSherpaConfig(a.cfg); err != nil {
			return err
		}
		if a.modelChecker != nil {
			a.logDebug("checking model", "session", session)
			if err := a.modelChecker(resolution.Engine); err != nil {
				return err
			}
		}
	}
	a.logInfo("asr load start", "session", session, "engine", resolution.Engine, "provider", resolution.Provider)
	if err := engine.Load(ctx); err != nil {
		if a.cfg.ASR.Engine != asr.EngineAuto || resolution.Engine != asr.EngineWhisper || a.asrFallback == nil {
			return err
		}
		a.logWarn("whisper-cpp load failed", "error", err)
		_ = engine.Close()
		fallback, fallbackResolution, fallbackErr := a.asrFallback()
		if fallbackErr != nil {
			return fmt.Errorf("whisper-cpp load failed: %v; sherpa fallback resolution failed: %w", err, fallbackErr)
		}
		fallbackResolution.FallbackReason = fmt.Sprintf("whisper-cpp load failed: %v", err)
		a.logWarn("falling back to sherpa-onnx", "reason", fallbackResolution.FallbackReason)
		a.setASREngine(fallback, fallbackResolution)
		engine = fallback
		resolution = fallbackResolution
		if configErr := validateResolvedSherpaConfig(a.cfg); configErr != nil {
			return configErr
		}
		if a.modelChecker != nil {
			a.logDebug("checking model", "session", session)
			if checkErr := a.modelChecker(asr.EngineSherpa); checkErr != nil {
				return checkErr
			}
		}
		a.logInfo("asr load start", "session", session, "engine", resolution.Engine, "provider", resolution.Provider)
		if fallbackErr = engine.Load(ctx); fallbackErr != nil {
			return fmt.Errorf("sherpa fallback load failed: %w", fallbackErr)
		}
	}
	a.confirmASRBackend(engine, resolution)
	a.logInfo("asr load complete", "session", session, "engine", a.asrResolution.Engine, "provider", a.asrResolution.Provider, "gpu_name", a.asrResolution.GPUName)
	return nil
}

func validateResolvedSherpaConfig(cfg config.Config) error {
	cfg.ASR.Engine = asr.EngineSherpa
	cfg.ASR.Provider = asr.ProviderCPU
	return cfg.ValidateASR()
}

func resolvedModelName(cfg config.Config, resolution asr.Resolution) string {
	if resolution.Engine == asr.EngineWhisper {
		return cfg.ASR.WhisperModel
	}
	return config.DefaultModelName
}

// CloseASR releases the current engine's native resources (VRAM for whisper).
// Call after the control server stops; the worker has exited by then.
func (a *App) CloseASR() error {
	a.mu.Lock()
	engine := a.engine
	a.engine = nil
	a.status.ASR.Loaded = false
	a.mu.Unlock()
	if engine == nil {
		return nil
	}
	return engine.Close()
}

func (a *App) setASREngine(engine asr.Engine, resolution asr.Resolution) {
	a.mu.Lock()
	a.engine = engine
	a.asrResolution = resolution
	a.status.ASR.ResolvedEngine = resolution.Engine
	a.status.ASR.ResolvedProvider = resolution.Provider
	a.status.ASR.GPUName = resolution.GPUName
	a.status.ASR.FallbackReason = resolution.FallbackReason
	a.status.ASR.Loaded = engine != nil && engine.Loaded()
	a.mu.Unlock()
}

func (a *App) confirmASRBackend(engine asr.Engine, resolution asr.Resolution) {
	if resolution.Engine != asr.EngineWhisper {
		a.setASREngine(engine, resolution)
		return
	}
	reporter, ok := engine.(asr.BackendReporter)
	name, gpu := "", false
	if ok {
		name, gpu = reporter.ActiveBackend()
	}
	if gpu {
		resolution.GPUName = name
	} else if resolution.Provider != asr.ProviderCPU {
		a.logWarn("whisper-cpp backend downgraded", "requested_provider", resolution.Provider, "reported_backend", name)
		resolution.Provider = asr.ProviderCPU
		resolution.GPUName = ""
	}
	a.setASREngine(engine, resolution)
}

func (a *App) Toggle(ctx context.Context) error {
	a.mu.Lock()
	active := a.status.State != api.StateIdle && a.status.State != api.StateError
	mode := a.status.Mode
	a.mu.Unlock()
	if active {
		if mode == nil || *mode != api.ModeToggle {
			return withCode("busy", fmt.Errorf("cannot toggle while %s dictation is active", modeName(mode)))
		}
		return a.Stop(ctx, true)
	}
	return a.Start(ctx, api.ModeToggle)
}

func (a *App) Status(ctx context.Context) api.Status {
	a.mu.Lock()
	st := a.status
	st.UptimeSeconds = time.Since(a.startedAt).Seconds()
	if a.source != nil {
		src := a.source.Stats()
		if src.Backend != "" {
			st.Audio.Backend = src.Backend
		}
		st.Audio.LevelDBFS = src.LevelDBFS
		st.Audio.Overruns = src.Overruns
		st.Audio.Capturing = src.Capturing
		st.Audio.DeviceID = src.DeviceID
		st.Audio.DeviceName = src.DeviceName
		st.Audio.InputLatency = src.InputLatency
		st.Audio.InputLatencyMS = float64(src.InputLatency) / float64(time.Millisecond)
		if src.SampleRate != 0 {
			st.Audio.SampleRate = src.SampleRate
		}
	}
	if a.engine != nil {
		st.ASR.Loaded = a.engine.Loaded()
	}
	a.mu.Unlock()
	if a.injector != nil {
		ictx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
		err := a.injector.Available(ictx)
		cancel()
		st.Injection.Available = err == nil
		if err != nil {
			st.Injection.LastError = err.Error()
		} else {
			st.Injection.LastError = ""
		}
	}
	if a.focus != nil {
		fctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
		target, err := a.focus.Current(fctx)
		cancel()
		if err == nil {
			st.Focus = statusForTarget(a.focus, target)
			a.focus.Release(target)
		} else {
			st.Focus.Available = false
		}
	}
	if a.permissionSource != nil {
		pctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
		snapshot, err := a.permissionSource.Snapshot(pctx)
		cancel()
		if err == nil {
			st.Permissions = permissionStatus(snapshot)
		}
	}
	if a.hotkey != nil && st.Hotkey != nil {
		hctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
		err := a.hotkey.Available(hctx)
		cancel()
		status := a.hotkey.Status()
		copy := *st.Hotkey
		copy.Available = err == nil
		if status.LastErrorCode != "" {
			copy.LastError = status.LastErrorCode
		} else if err != nil {
			copy.LastError = err.Error()
		} else {
			copy.LastError = ""
		}
		st.Hotkey = &copy
	}
	return st
}

func permissionStatus(snapshot permissions.Snapshot) *api.PermissionStatus {
	return &api.PermissionStatus{
		Microphone:      string(snapshot.Microphone),
		Accessibility:   string(snapshot.Accessibility),
		InputMonitoring: string(snapshot.InputMonitoring),
	}
}

func (a *App) captureLoop(ctx context.Context) {
	buf := make([]float32, a.cfg.Audio.SampleRate*a.cfg.Audio.QuantumMS/1000)
	if len(buf) == 0 {
		buf = make([]float32, 320)
	}
	start := time.Now()
	reason := "return"
	a.logDebug("capture loop start", "sample_rate", a.cfg.Audio.SampleRate, "quantum_ms", a.cfg.Audio.QuantumMS, "buffer_frames", len(buf))
	defer func() {
		a.logDebug("capture loop exit", "reason", reason, "elapsed_ms", time.Since(start).Milliseconds())
	}()
	var prevOverruns uint64
	for {
		// A paused source may return no frames, so cancellation must be checked
		// before every read to prevent a stale loop racing the segmenter.
		select {
		case <-ctx.Done():
			reason = "context_done"
			return
		default:
		}
		a.mu.Lock()
		src := a.source
		capturing := a.capturing
		a.mu.Unlock()
		if !capturing {
			// Stop cleared this; exit even if an interleaved Start left the
			// context live, so no orphan loop touches the segmenter.
			reason = "not_capturing"
			return
		}
		if src == nil {
			reason = "source_nil"
			a.recordError(api.StateIdle, apperr.CodeAudioBackendUnavailable, audio.ErrUnavailable)
			return
		}
		n, err := src.Read(ctx, buf)
		if err != nil {
			if ctx.Err() == nil {
				if a.segmenter != nil {
					a.segMu.Lock()
					a.segmenter.Reset()
					a.segMu.Unlock()
				}
				reason = "read_error"
				a.logWarn("audio read failed", "error", err)
				err = normalizeError(apperr.CodeAudioDeviceDisconnected, "read audio capture", err)
				a.recordError(api.StateIdle, apperr.Code(err), err)
				a.scheduleAudioRetry()
			}
			if ctx.Err() != nil {
				reason = "context_done"
			}
			return
		}
		if n == 0 {
			continue
		}
		if stats := src.Stats(); stats.Overruns > prevOverruns {
			a.logWarn("audio capture overrun", "overruns", stats.Overruns, "previous_overruns", prevOverruns, "sample_rate", stats.SampleRate)
			prevOverruns = stats.Overruns
			if marker, ok := a.segmenter.(interface{ MarkCaptureOverrun() }); ok {
				a.segMu.Lock()
				marker.MarkCaptureOverrun()
				a.segMu.Unlock()
			}
		}
		now := time.Now()
		if audio.LevelDBFS(buf[:n]) > -45 {
			a.touchActivity(now)
		}
		if a.segmenter == nil {
			continue
		}
		a.segMu.Lock()
		segs := a.segmenter.Feed(buf[:n], now)
		a.segMu.Unlock()
		if len(segs) > 0 {
			a.logDebug("segmenter produced segments", "segments", len(segs), "input_frames", n)
		}
		for _, seg := range segs {
			a.queueSegment(seg)
		}
		a.updateSegmentState()
	}
}

func (a *App) queueSegment(seg asr.AudioSegment) {
	a.mu.Lock()
	session := a.currentSession
	if _, ok := a.discarded[session]; ok {
		a.mu.Unlock()
		a.logDebug("segment discarded before queue", append([]any{"session", session}, segmentLogAttrs(seg)...)...)
		return
	}
	a.status.State = api.StateRecognizing
	a.lastActivity = time.Now()
	a.pendingASR++
	a.pendingSession[session]++
	pending := a.pendingASR
	a.mu.Unlock()
	if isShortSegment(seg) {
		a.logWarn("short audio segment queued", append([]any{"session", session, "pending_asr", pending}, segmentLogAttrs(seg)...)...)
	} else {
		a.logDebug("audio segment queued", append([]any{"session", session, "pending_asr", pending}, segmentLogAttrs(seg)...)...)
	}
	job := segmentJob{session: session, segment: seg}
	select {
	case a.asrQueue <- job:
		a.logDebug("asr queue accepted segment", append([]any{"session", session, "queue_len", len(a.asrQueue)}, segmentLogAttrs(seg)...)...)
	case <-a.rootCtx.Done():
		a.finishASRJob(session)
		a.logWarn("segment dropped because root context ended", append([]any{"session", session}, segmentLogAttrs(seg)...)...)
	default:
		a.finishASRJob(session)
		a.logWarn("segment dropped because asr queue is full", append([]any{"session", session, "queue_len", len(a.asrQueue)}, segmentLogAttrs(seg)...)...)
		a.recordError(a.nextState(), "recognition_failed", fmt.Errorf("recognition queue full; dropped segment %s", seg.ID))
	}
}

func (a *App) autoStopLoop(ctx context.Context) {
	timeout := time.Duration(a.cfg.Daemon.AutoStopAfterSilenceSeconds) * time.Second
	if timeout <= 0 {
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.mu.Lock()
			idleFor := time.Since(a.lastActivity)
			capturing := a.capturing
			a.mu.Unlock()
			if capturing && idleFor >= timeout {
				a.logInfo("auto stop after silence", "idle_ms", idleFor.Milliseconds(), "timeout_ms", timeout.Milliseconds())
				_ = a.Stop(context.Background(), true)
				return
			}
		}
	}
}

func (a *App) touchActivity(now time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.lastActivity = now
}

func (a *App) recreateSource(ctx context.Context) error {
	if a.sourceFactory == nil && a.audioSourceFactory == nil {
		return audio.ErrUnavailable
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	a.mu.Lock()
	audioCfg := a.cfg.Audio
	factory := a.audioSourceFactory
	legacyFactory := a.sourceFactory
	a.mu.Unlock()
	var (
		src audio.Source
		err error
	)
	if factory != nil {
		src, err = factory(audioCfg)
	} else {
		src, err = legacyFactory()
	}
	if err != nil {
		a.logWarn("audio source recreate failed", "error", err)
		return normalizeError(apperr.CodeAudioBackendUnavailable, "create audio source", err)
	}
	if src == nil {
		return audio.ErrUnavailable
	}
	stats := src.Stats()
	a.mu.Lock()
	old := a.source
	a.source = src
	a.status.Audio.Capturing = false
	if stats.Backend != "" {
		a.status.Audio.Backend = stats.Backend
	}
	a.status.Audio.SampleRate = stats.SampleRate
	a.status.Audio.DeviceID = stats.DeviceID
	a.status.Audio.DeviceName = stats.DeviceName
	a.status.Audio.InputLatency = stats.InputLatency
	a.status.Audio.InputLatencyMS = float64(stats.InputLatency) / float64(time.Millisecond)
	a.mu.Unlock()
	closeSource(old)
	a.logDebug("audio source recreated", "backend", stats.Backend, "sample_rate", stats.SampleRate)
	return nil
}

func (a *App) scheduleAudioRetry() {
	a.mu.Lock()
	if a.retryingAudio || (a.sourceFactory == nil && a.audioSourceFactory == nil) {
		a.mu.Unlock()
		return
	}
	a.retryingAudio = true
	a.mu.Unlock()
	go func() {
		defer func() {
			a.mu.Lock()
			a.retryingAudio = false
			a.mu.Unlock()
		}()
		backoff := time.Second
		for {
			select {
			case <-a.rootCtx.Done():
				return
			case <-time.After(backoff):
			}
			a.mu.Lock()
			capturing := a.capturing
			a.mu.Unlock()
			if capturing {
				a.logDebug("audio retry stopped because capture resumed")
				return
			}
			if err := a.recreateSource(a.rootCtx); err == nil {
				a.mu.Lock()
				a.status.LastError = nil
				a.mu.Unlock()
				a.logInfo("audio source retry succeeded")
				return
			}
			a.logWarn("audio source retry failed", "next_backoff_ms", backoff.Milliseconds())
			if backoff < 30*time.Second {
				backoff *= 2
			}
		}
	}()
}

func closeSource(src audio.Source) {
	if closer, ok := src.(interface{ Close() }); ok {
		closer.Close()
	}
}

func (a *App) clearSource(src audio.Source) {
	a.mu.Lock()
	a.source = nil
	a.status.Audio.Capturing = false
	a.mu.Unlock()
	closeSource(src)
}

func (a *App) asrWorker(ctx context.Context) {
	a.logDebug("asr worker start")
	defer a.logDebug("asr worker exit")
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-a.asrQueue:
			if a.sessionDiscarded(job.session) {
				a.logDebug("asr job skipped because session is discarded", append([]any{"session", job.session}, segmentLogAttrs(job.segment)...)...)
				a.finishASRJob(job.session)
				continue
			}
			a.handleSegment(ctx, job)
			a.finishASRJob(job.session)
		}
	}
}

func (a *App) handleSegment(ctx context.Context, job segmentJob) {
	seg := job.segment
	if a.engine == nil {
		a.recordError(a.nextState(), "recognition_failed", fmt.Errorf("ASR engine is unavailable"))
		return
	}
	if a.sessionDiscarded(job.session) {
		return
	}
	if err := a.saveDebugSegment(seg); err != nil {
		a.recordError(a.nextState(), "audio_save_failed", err)
	}
	a.setState(api.StateRecognizing)
	tctx, cancel := context.WithTimeout(ctx, asrTimeout(seg.Duration))
	timeout := asrTimeout(seg.Duration)
	a.logInfo("asr decode start", append([]any{"session", job.session, "timeout_ms", timeout.Milliseconds()}, segmentLogAttrs(seg)...)...)
	tr, err := a.engine.Transcribe(tctx, seg)
	cancel()
	if err != nil {
		if a.sessionDiscarded(job.session) {
			return
		}
		a.logWarn("asr decode failed", append([]any{"session", job.session, "error", err}, segmentLogAttrs(seg)...)...)
		a.recordError(a.nextState(), recognitionErrorCode(err), err)
		return
	}
	if a.sessionDiscarded(job.session) {
		return
	}
	a.mu.Lock()
	a.status.ASR.LastRTF = tr.RealTimeFactor
	a.mu.Unlock()
	a.logInfo("asr decode complete",
		"session", job.session,
		"segment_id", seg.ID,
		"empty", tr.Empty,
		"text_bytes", len(tr.Text),
		"audio_ms", tr.AudioDuration.Milliseconds(),
		"decode_ms", tr.DecodeDuration.Milliseconds(),
		"rtf", tr.RealTimeFactor)
	if tr.Empty {
		a.setState(a.nextState())
		return
	}
	if a.bufferDeferredTranscript(job.session, tr.Text) {
		a.setState(a.nextState())
		return
	}
	a.mu.Lock()
	post := a.post
	caseState := a.caseState
	a.mu.Unlock()
	text, next := post.Apply(tr.Text, caseState)
	if text == "" {
		a.logDebug("postprocess produced empty text", "session", job.session, "segment_id", seg.ID)
		a.setState(a.nextState())
		return
	}
	var focusWarning *focus.Change
	if a.injector != nil {
		a.setState(api.StateTyping)
		a.logDebug("typing transcript", "session", job.session, "segment_id", seg.ID, "text_bytes", len(text), "redacted", a.cfg.Daemon.RedactTranscriptsInLogs)
		var err error
		focusWarning, err = a.injectText(ctx, text)
		if err != nil {
			if isFocusError(err) {
				a.logWarn("focus guard cancelled injection", "session", job.session, "segment_id", seg.ID, "error", err)
				a.recordCanceledTranscript(text, err)
			} else {
				a.recordUninjected(text, err)
			}
			a.setState(a.nextState())
			return
		}
		a.mu.Lock()
		if job.session == a.currentSession {
			if _, discarded := a.discarded[job.session]; !discarded {
				a.caseState = next
			}
		}
		a.mu.Unlock()
	}
	a.recordTranscript(text)
	if focusWarning != nil {
		a.recordWarning(apperr.CodeFocusChanged, focusWarning.Error())
	}
	a.finishModeAfterSegment(ctx)
}

func (a *App) injectText(ctx context.Context, text string) (*focus.Change, error) {
	var target focus.Target
	var warning *focus.Change
	if a.guard != nil && a.cfg.Focus.Enabled {
		var err error
		target, warning, err = a.guard.ResolveForInjection(ctx)
		if err != nil {
			return nil, err
		}
		defer a.focus.Release(target)
	}
	request := inject.Request{
		Text:     text,
		Target:   inject.Target{Focus: target},
		KeyDelay: time.Duration(a.cfg.Injection.KeyDelayMS) * time.Millisecond,
	}
	if a.cfg.Injection.TimeoutMS > 0 {
		request.Deadline = time.Now().Add(time.Duration(a.cfg.Injection.TimeoutMS) * time.Millisecond)
	}
	if target.Token != 0 || target.DegradedReason != "" {
		request.ValidateTarget = func(ctx context.Context, target focus.Target) error {
			return focus.ValidateTarget(ctx, a.focus, target)
		}
	}
	if err := a.injector.Inject(ctx, request); err != nil {
		return warning, normalizeError(apperr.CodeInjectionFailed, "inject transcript", err)
	}
	return warning, nil
}

func (a *App) finishModeAfterSegment(ctx context.Context) {
	a.mu.Lock()
	mode := a.status.Mode
	a.mu.Unlock()
	if mode != nil && *mode == api.ModeOneshot {
		a.logDebug("oneshot segment complete; stopping")
		_ = a.Stop(ctx, false)
		return
	}
	a.setState(a.nextState())
}

func (a *App) saveDebugSegment(seg asr.AudioSegment) error {
	if !a.cfg.Debug.SaveAudioSegments {
		return nil
	}
	if err := os.MkdirAll(a.cfg.Debug.SaveAudioDir, 0700); err != nil {
		return err
	}
	name := safeSegmentFilename(seg.ID) + ".wav"
	path := filepath.Join(a.cfg.Debug.SaveAudioDir, name)
	if err := audio.WriteWAVFloat32(path, seg.Samples, seg.SampleRate); err != nil {
		return err
	}
	a.logDebug("debug audio segment saved", append([]any{"path", path}, segmentLogAttrs(seg)...)...)
	return nil
}

func safeSegmentFilename(id string) string {
	if id == "" {
		return "segment"
	}
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "segment"
	}
	return b.String()
}

func recognitionErrorCode(err error) string {
	return apperr.CodeRecognitionFailed
}

func (a *App) nextState() api.State {
	a.mu.Lock()
	defer a.mu.Unlock()
	if deferred := a.deferred[a.currentSession]; deferred != nil && deferred.finalizing {
		return api.StateTyping
	}
	if a.pendingSession[a.currentSession] > 0 {
		return api.StateRecognizing
	}
	if a.capturing {
		if a.segmentOpen {
			return api.StateSegmentOpen
		}
		return api.StateListening
	}
	return api.StateIdle
}

func (a *App) updateSegmentState() {
	reporter, ok := a.segmenter.(interface{ SegmentOpen() bool })
	if !ok {
		return
	}
	a.segMu.Lock()
	open := reporter.SegmentOpen()
	a.segMu.Unlock()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.segmentOpen = open
	if !a.capturing {
		return
	}
	switch a.status.State {
	case api.StateListening, api.StateSegmentOpen:
		if open {
			a.status.State = api.StateSegmentOpen
		} else {
			a.status.State = api.StateListening
		}
	}
}

func (a *App) sessionDiscarded(session uint64) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	_, ok := a.discarded[session]
	return ok
}

func (a *App) finishASRJob(session uint64) {
	a.mu.Lock()
	if a.pendingASR > 0 {
		a.pendingASR--
	}
	if a.pendingSession[session] > 0 {
		a.pendingSession[session]--
	}
	remaining := a.pendingSession[session]
	if remaining == 0 {
		delete(a.pendingSession, session)
	}
	if _, discarded := a.discarded[session]; discarded && remaining == 0 {
		delete(a.discarded, session)
		delete(a.deferred, session)
	}
	finalize := a.beginDeferredFinalizeLocked(session)
	if session == a.currentSession && !finalize {
		switch {
		case a.deferred[session] != nil && a.deferred[session].finalizing:
			a.status.State = api.StateTyping
		case remaining > 0:
			a.status.State = api.StateRecognizing
		case a.capturing && a.segmentOpen:
			a.status.State = api.StateSegmentOpen
		case a.capturing:
			a.status.State = api.StateListening
		case a.deferred[session] != nil && a.deferred[session].commitRequested:
			a.status.State = api.StateTyping
		default:
			a.status.State = api.StateIdle
			a.status.Mode = nil
		}
	}
	resetFocus := session == a.currentSession && a.status.State == api.StateIdle
	a.mu.Unlock()
	if resetFocus && a.guard != nil {
		a.guard.Reset()
	}
	if finalize {
		go a.finalizeDeferred(session)
	}
}

func (a *App) setState(state api.State) {
	a.mu.Lock()
	a.status.State = state
	if state == api.StateIdle {
		a.status.Mode = nil
		a.segmentOpen = false
	}
	a.mu.Unlock()
	if state == api.StateIdle && a.guard != nil {
		a.guard.Reset()
	}
}

func (a *App) recordError(state api.State, code string, err error) {
	a.mu.Lock()
	a.status.State = state
	a.status.LastError = &api.ErrorInfo{Code: code, Message: err.Error()}
	a.status.LastWarning = nil
	if state == api.StateIdle || state == api.StateError {
		a.status.Mode = nil
		a.capturing = false
		a.segmentOpen = false
		a.status.Audio.Capturing = false
	}
	logger := a.logger
	a.mu.Unlock()
	if (state == api.StateIdle || state == api.StateError) && a.guard != nil {
		a.guard.Reset()
	}
	if logger != nil {
		logger.Warn("state error recorded", "state", state, "code", code, "error", err)
	}
}

func (a *App) recordFocus(target *focus.Target) {
	if target == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.status.Focus = statusForTarget(a.focus, *target)
}

func (a *App) recordTranscript(text string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.status.LastError = nil
	a.status.LastWarning = nil
	a.status.LastUninjectedText = ""
	a.status.LastTranscriptRedacted = a.cfg.Daemon.RedactTranscriptsInLogs
	if a.cfg.Daemon.RedactTranscriptsInLogs {
		a.status.LastTranscript = ""
	} else {
		a.status.LastTranscript = text
	}
	if a.logger != nil {
		a.logger.Info("transcript accepted", "text_bytes", len(text), "redacted", a.cfg.Daemon.RedactTranscriptsInLogs)
	}
}

func (a *App) recordCanceledTranscript(text string, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	code := apperr.Code(err)
	a.status.LastError = &api.ErrorInfo{Code: code, Message: err.Error()}
	a.status.LastWarning = nil
	a.status.LastTranscriptRedacted = a.cfg.Daemon.RedactTranscriptsInLogs
	if !a.cfg.Daemon.RedactTranscriptsInLogs {
		a.status.LastUninjectedText = text
	} else {
		a.status.LastUninjectedText = ""
	}
	if a.logger != nil {
		a.logger.Warn("transcript cancelled", "code", code, "text_bytes", len(text), "redacted", a.cfg.Daemon.RedactTranscriptsInLogs, "error", err)
	}
}

func (a *App) recordUninjected(text string, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	code := apperr.Code(err)
	a.status.LastError = &api.ErrorInfo{Code: code, Message: err.Error()}
	a.status.LastWarning = nil
	a.status.LastTranscriptRedacted = a.cfg.Daemon.RedactTranscriptsInLogs
	if !a.cfg.Daemon.RedactTranscriptsInLogs {
		a.status.LastUninjectedText = text
	} else {
		a.status.LastUninjectedText = ""
	}
	if a.logger != nil {
		a.logger.Warn("transcript not injected", "code", code, "text_bytes", len(text), "redacted", a.cfg.Daemon.RedactTranscriptsInLogs, "error", err)
	}
}

func (a *App) recordWarning(code, message string) {
	a.mu.Lock()
	a.status.LastWarning = &api.ErrorInfo{Code: code, Message: message}
	logger := a.logger
	a.mu.Unlock()
	if logger != nil {
		logger.Warn(message, "code", code)
	}
}

func (a *App) logDebug(msg string, args ...any) {
	if a.logger != nil {
		a.logger.Debug(msg, args...)
	}
}

func (a *App) logInfo(msg string, args ...any) {
	if a.logger != nil {
		a.logger.Info(msg, args...)
	}
}

func (a *App) logWarn(msg string, args ...any) {
	if a.logger != nil {
		a.logger.Warn(msg, args...)
	}
}

func segmentLogAttrs(seg asr.AudioSegment) []any {
	return []any{
		"segment_id", seg.ID,
		"samples", len(seg.Samples),
		"sample_rate", seg.SampleRate,
		"duration_ms", seg.Duration.Milliseconds(),
		"capture_overrun", seg.CaptureOverrun,
		"degraded", seg.Degraded,
	}
}

func isShortSegment(seg asr.AudioSegment) bool {
	sampleRate := seg.SampleRate
	if sampleRate <= 0 {
		sampleRate = 16000
	}
	return len(seg.Samples) < sampleRate/10
}

func stringArg(args map[string]any, name string) string {
	if args == nil {
		return ""
	}
	v, _ := args[name].(string)
	return v
}

func boolArg(args map[string]any, name string) bool {
	if args == nil {
		return false
	}
	v, _ := args[name].(bool)
	return v
}

func withCode(code string, err error) error {
	if err == nil {
		return nil
	}
	return apperr.New(code, "app request", err)
}

func codeFor(err error) string {
	return apperr.Code(err)
}

func normalizeError(code, operation string, err error) error {
	if err == nil {
		return nil
	}
	if apperr.Code(err) != apperr.CodeInternalError {
		return err
	}
	return apperr.New(code, operation, err)
}

func isFocusError(err error) bool {
	switch apperr.Code(err) {
	case apperr.CodeFocusUnavailable, apperr.CodeFocusChanged, apperr.CodeSecureField:
		return true
	default:
		return false
	}
}

func injectorBackend(injector inject.Injector, fallback string) string {
	if injector != nil && injector.Backend() != "" {
		return injector.Backend()
	}
	return fallback
}

func focusBackend(provider focus.Provider) string {
	if provider == nil {
		return ""
	}
	return provider.Backend()
}

func statusForTarget(provider focus.Provider, target focus.Target) api.FocusStatus {
	status := api.FocusStatus{
		Backend:        target.Backend,
		Available:      true,
		AppID:          target.AppID,
		AppName:        target.AppName,
		PID:            target.PID,
		SecureField:    target.SecureField,
		DegradedReason: target.DegradedReason,
		Sway:           target.Backend == "sway",
	}
	if target.Backend == "sway" {
		status.StableID = target.StableID
	}
	if metadataProvider, ok := provider.(focus.MetadataProvider); ok {
		metadata := metadataProvider.Metadata(target)
		status.FocusedID = metadata.FocusedID
		status.FocusedName = metadata.FocusedName
		status.Class = metadata.Class
		status.Workspace = metadata.Workspace
		status.Output = metadata.Output
	}
	return status
}
