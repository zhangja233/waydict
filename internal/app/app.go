package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"waydict/internal/asr"
	"waydict/internal/audio"
	"waydict/internal/config"
	"waydict/internal/control"
	"waydict/internal/inject"
	"waydict/internal/swayipc"
	"waydict/internal/vad"
	"waydict/pkg/api"
)

type Dependencies struct {
	Source         audio.Source
	SourceFactory  func() (audio.Source, error)
	ModelChecker   func() error
	ConfigReloader func(context.Context) (config.Config, error)
	Segmenter      vad.Segmenter
	Engine         asr.Engine
	Injector       inject.Injector
	Focus          *swayipc.Client
	Logger         *slog.Logger
	Shutdown       func()
}

type App struct {
	cfg            config.Config
	source         audio.Source
	sourceFactory  func() (audio.Source, error)
	modelChecker   func() error
	configReloader func(context.Context) (config.Config, error)
	segmenter      vad.Segmenter
	engine         asr.Engine
	injector       inject.Injector
	guard          *swayipc.Guard
	focus          *swayipc.Client
	logger         *slog.Logger
	post           inject.PostProcessor

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
	segmentOpen    bool
}

type segmentJob struct {
	session uint64
	segment asr.AudioSegment
}

func New(ctx context.Context, cfg config.Config, deps Dependencies) *App {
	a := &App{
		cfg:            cfg,
		source:         deps.Source,
		sourceFactory:  deps.SourceFactory,
		modelChecker:   deps.ModelChecker,
		configReloader: deps.ConfigReloader,
		segmenter:      deps.Segmenter,
		engine:         deps.Engine,
		injector:       deps.Injector,
		focus:          deps.Focus,
		logger:         deps.Logger,
		post:           inject.NewPostProcessor(cfg.PostProcess, cfg.Injection.AppendSpace),
		startedAt:      time.Now(),
		rootCtx:        ctx,
		asrQueue:       make(chan segmentJob, 8),
		shutdown:       deps.Shutdown,
		discarded:      make(map[uint64]struct{}),
	}
	if deps.Focus != nil {
		a.guard = swayipc.NewGuard(deps.Focus, cfg.Injection.FocusPolicy)
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
			Engine:     cfg.ASR.Engine,
			Model:      config.DefaultModelName,
			Provider:   cfg.ASR.Provider,
			NumThreads: cfg.ASR.NumThreads,
			Loaded:     deps.Engine != nil && deps.Engine.Loaded(),
		},
		Injection: api.InjectionStatus{
			Engine: cfg.Injection.Engine,
		},
		Focus: api.FocusStatus{
			Sway: deps.Focus != nil,
		},
		LastTranscriptRedacted: cfg.Daemon.RedactTranscriptsInLogs,
	}
	go a.asrWorker(ctx)
	return a
}

func (a *App) HandleControl(ctx context.Context, req control.Request) control.Response {
	if req.Command != "status" {
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
		return control.Fail(req.ID, "usage", "unsupported command", a.Status(ctx))
	}
}

func (a *App) Start(ctx context.Context, mode api.Mode) error {
	a.mu.Lock()
	if a.status.State != api.StateIdle && a.status.State != api.StateError {
		state := a.status.State
		a.mu.Unlock()
		a.logDebug("start ignored", "mode", mode, "state", state)
		return nil
	}
	a.status.State = api.StateArming
	a.status.Mode = modePtr(mode)
	a.status.LastError = nil
	a.status.LastWarning = nil
	a.currentSession++
	session := a.currentSession
	a.mu.Unlock()
	a.logInfo("start requested", "session", session, "mode", mode)

	if a.injector != nil {
		a.logDebug("checking injector", "session", session)
		if err := a.injector.Available(ctx); err != nil {
			a.recordError(api.StateIdle, "wtype_unavailable", err)
			return withCode("wtype_unavailable", err)
		}
	}
	if a.engine != nil && !a.engine.Loaded() {
		if a.modelChecker != nil {
			a.logDebug("checking model", "session", session)
			if err := a.modelChecker(); err != nil {
				a.recordError(api.StateIdle, "model_invalid", err)
				return withCode("model_invalid", err)
			}
		}
		a.logInfo("asr load start", "session", session)
		if err := a.engine.Load(ctx); err != nil {
			a.recordError(api.StateIdle, "model_invalid", err)
			return withCode("model_invalid", err)
		}
		a.logInfo("asr load complete", "session", session)
	}
	if a.guard != nil && a.cfg.Sway.FocusCheck {
		if err := a.guard.CaptureStart(ctx); err != nil {
			a.recordError(api.StateIdle, "sway_unavailable", err)
			return withCode("sway_unavailable", err)
		}
		a.recordFocus(a.guard.StartedFocus())
	}
	a.mu.Lock()
	src := a.source
	a.mu.Unlock()
	if src == nil {
		a.logInfo("audio source recreate start", "session", session)
		if err := a.recreateSource(ctx); err != nil {
			a.recordError(api.StateIdle, "pipewire_unavailable", err)
			return withCode("pipewire_unavailable", err)
		}
		a.logInfo("audio source recreate complete", "session", session)
	}
	a.mu.Lock()
	src = a.source
	a.mu.Unlock()
	if src == nil {
		err := audio.ErrUnavailable
		a.recordError(api.StateIdle, "pipewire_unavailable", err)
		return withCode("pipewire_unavailable", err)
	}
	a.logDebug("audio source start", "session", session)
	if err := src.Start(ctx); err != nil {
		a.clearSource(src)
		a.recordError(api.StateIdle, "pipewire_unavailable", err)
		return withCode("pipewire_unavailable", err)
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
	a.mu.Unlock()
	a.logInfo("capture started", "session", session)
	go func() {
		defer close(captureDone)
		a.captureLoop(sessionCtx)
	}()
	go a.autoStopLoop(sessionCtx)
	return nil
}

func (a *App) Stop(ctx context.Context, commit bool) error {
	a.mu.Lock()
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
			a.recordError(api.StateIdle, "pipewire_unavailable", err)
			return withCode("pipewire_unavailable", err)
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
	if !commit || a.pendingASR == 0 {
		a.status.State = api.StateIdle
		a.status.Mode = nil
	}
	a.mu.Unlock()
	return nil
}

func (a *App) ReloadConfig(ctx context.Context) error {
	if a.configReloader == nil {
		return withCode("reload_unavailable", fmt.Errorf("config reload is unavailable"))
	}
	a.mu.Lock()
	if a.capturing || a.pendingASR > 0 {
		a.mu.Unlock()
		return withCode("busy", fmt.Errorf("cannot reload config while dictation is active"))
	}
	a.mu.Unlock()

	cfg, err := a.configReloader(ctx)
	if err != nil {
		return withCode("config_invalid", err)
	}
	if err := cfg.Validate(); err != nil {
		return withCode("config_invalid", err)
	}

	a.mu.Lock()
	if a.capturing || a.pendingASR > 0 {
		a.mu.Unlock()
		return withCode("busy", fmt.Errorf("cannot reload config while dictation is active"))
	}
	if err := reloadRequiresRestart(a.cfg, cfg); err != nil {
		a.mu.Unlock()
		return withCode("restart_required", err)
	}
	a.cfg = cfg
	a.post = inject.NewPostProcessor(cfg.PostProcess, cfg.Injection.AppendSpace)
	a.status.LastTranscriptRedacted = cfg.Daemon.RedactTranscriptsInLogs
	if cfg.Daemon.RedactTranscriptsInLogs {
		a.status.LastTranscript = ""
		a.status.LastUninjectedText = ""
	}
	a.mu.Unlock()
	a.logInfo("config reloaded",
		"log_level", cfg.Daemon.LogLevel,
		"redacted_transcripts", cfg.Daemon.RedactTranscriptsInLogs,
		"auto_stop_seconds", cfg.Daemon.AutoStopAfterSilenceSeconds,
		"append_space", cfg.Injection.AppendSpace,
		"debug_save_audio", cfg.Debug.SaveAudioSegments)
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
		return fmt.Errorf("audio changes require restart")
	}
	if old.VAD != next.VAD {
		return fmt.Errorf("VAD changes require restart")
	}
	if old.ASR != next.ASR {
		return fmt.Errorf("ASR changes require restart")
	}
	oldInjection := old.Injection
	nextInjection := next.Injection
	oldInjection.AppendSpace = nextInjection.AppendSpace
	if oldInjection != nextInjection {
		return fmt.Errorf("injection engine changes require restart")
	}
	if old.Sway != next.Sway {
		return fmt.Errorf("Sway changes require restart")
	}
	return nil
}

func (a *App) Toggle(ctx context.Context) error {
	a.mu.Lock()
	active := a.status.State != api.StateIdle && a.status.State != api.StateError
	a.mu.Unlock()
	if active {
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
		st.Audio.LevelDBFS = src.LevelDBFS
		st.Audio.Overruns = src.Overruns
		st.Audio.Capturing = src.Capturing
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
		defer cancel()
		if f, err := a.focus.Focused(fctx); err == nil {
			st.Focus.Sway = true
			st.Focus.FocusedID = f.ID
			st.Focus.FocusedName = f.Name
			st.Focus.AppID = f.AppID
			st.Focus.Class = f.Class
			st.Focus.Workspace = f.Workspace
			st.Focus.Output = f.Output
		}
	}
	return st
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
		// pipewire Read returns (0, nil) on a paused source, so the loop must
		// check cancellation itself; otherwise it spins forever after Stop and
		// a later Start races a second captureLoop on the shared segmenter.
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
			a.recordError(api.StateIdle, "pipewire_unavailable", audio.ErrUnavailable)
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
				a.recordError(api.StateIdle, classify(err), err)
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
	if a.sourceFactory == nil {
		return audio.ErrUnavailable
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	src, err := a.sourceFactory()
	if err != nil {
		a.logWarn("audio source recreate failed", "error", err)
		return err
	}
	sampleRate := src.Stats().SampleRate
	a.mu.Lock()
	old := a.source
	a.source = src
	a.status.Audio.Capturing = false
	a.status.Audio.SampleRate = sampleRate
	a.mu.Unlock()
	closeSource(old)
	a.logDebug("audio source recreated", "sample_rate", sampleRate)
	return nil
}

func (a *App) scheduleAudioRetry() {
	a.mu.Lock()
	if a.retryingAudio || a.sourceFactory == nil {
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
		a.setState(a.nextState())
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
			a.setState(a.nextState())
			return
		}
		a.logWarn("asr decode failed", append([]any{"session", job.session, "error", err}, segmentLogAttrs(seg)...)...)
		a.recordError(a.nextState(), recognitionErrorCode(err), err)
		return
	}
	if a.sessionDiscarded(job.session) {
		a.setState(a.nextState())
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
	text := a.post.Apply(tr.Text)
	if text == "" {
		a.logDebug("postprocess produced empty text", "session", job.session, "segment_id", seg.ID)
		a.setState(a.nextState())
		return
	}
	var focusWarning *swayipc.FocusChange
	if a.guard != nil && a.cfg.Sway.FocusCheck {
		var err error
		focusWarning, err = a.guard.CheckWithWarning(ctx)
		if err != nil {
			a.logWarn("focus guard cancelled injection", "session", job.session, "segment_id", seg.ID, "error", err)
			a.recordCanceledTranscript(text, err)
			a.setState(a.nextState())
			return
		}
	}
	if a.injector != nil {
		a.setState(api.StateTyping)
		a.logDebug("typing transcript", "session", job.session, "segment_id", seg.ID, "text_bytes", len(text), "redacted", a.cfg.Daemon.RedactTranscriptsInLogs)
		if err := a.injector.TypeText(ctx, text); err != nil {
			a.recordUninjected(text, err)
			a.setState(a.nextState())
			return
		}
	}
	a.recordTranscript(text)
	if focusWarning != nil {
		a.recordWarning("focus_changed", focusWarning.Error())
	}
	a.finishModeAfterSegment(ctx)
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
	if errors.Is(err, context.DeadlineExceeded) {
		return "recognition_timeout"
	}
	return "recognition_failed"
}

func (a *App) nextState() api.State {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.pendingASR > 0 {
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
	defer a.mu.Unlock()
	if a.pendingASR > 0 {
		a.pendingASR--
	}
	if a.pendingASR == 0 {
		delete(a.discarded, session)
		switch a.status.State {
		case api.StateRecognizing, api.StateTyping:
			if a.capturing {
				if a.segmentOpen {
					a.status.State = api.StateSegmentOpen
				} else {
					a.status.State = api.StateListening
				}
			} else {
				a.status.State = api.StateIdle
				a.status.Mode = nil
			}
		}
	}
}

func (a *App) setState(state api.State) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.status.State = state
	if state == api.StateIdle {
		a.status.Mode = nil
		a.segmentOpen = false
	}
}

func (a *App) recordError(state api.State, code string, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.status.State = state
	a.status.LastError = &api.ErrorInfo{Code: code, Message: err.Error()}
	a.status.LastWarning = nil
	if state == api.StateIdle || state == api.StateError {
		a.status.Mode = nil
		a.capturing = false
		a.segmentOpen = false
		a.status.Audio.Capturing = false
	}
	if a.logger != nil {
		a.logger.Warn("state error recorded", "state", state, "code", code, "error", err)
	}
}

func (a *App) recordFocus(f *swayipc.FocusedContainer) {
	if f == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.status.Focus = api.FocusStatus{
		Sway:        true,
		FocusedID:   f.ID,
		FocusedName: f.Name,
		AppID:       f.AppID,
		Class:       f.Class,
		Workspace:   f.Workspace,
		Output:      f.Output,
	}
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
	a.status.LastError = &api.ErrorInfo{Code: "focus_changed", Message: err.Error()}
	a.status.LastWarning = nil
	a.status.LastTranscriptRedacted = a.cfg.Daemon.RedactTranscriptsInLogs
	if !a.cfg.Daemon.RedactTranscriptsInLogs {
		a.status.LastUninjectedText = text
	} else {
		a.status.LastUninjectedText = ""
	}
	if a.logger != nil {
		a.logger.Warn("transcript cancelled", "code", "focus_changed", "text_bytes", len(text), "redacted", a.cfg.Daemon.RedactTranscriptsInLogs, "error", err)
	}
}

func (a *App) recordUninjected(text string, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.status.LastError = &api.ErrorInfo{Code: "wtype_failed", Message: err.Error()}
	a.status.LastWarning = nil
	a.status.LastTranscriptRedacted = a.cfg.Daemon.RedactTranscriptsInLogs
	if !a.cfg.Daemon.RedactTranscriptsInLogs {
		a.status.LastUninjectedText = text
	} else {
		a.status.LastUninjectedText = ""
	}
	if a.logger != nil {
		a.logger.Warn("transcript not injected", "code", "wtype_failed", "text_bytes", len(text), "redacted", a.cfg.Daemon.RedactTranscriptsInLogs, "error", err)
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

type codedError struct {
	code string
	err  error
}

func (e codedError) Error() string {
	return e.err.Error()
}

func (e codedError) Unwrap() error {
	return e.err
}

func withCode(code string, err error) error {
	if err == nil {
		return nil
	}
	return codedError{code: code, err: err}
}

func codeFor(err error) string {
	var coded codedError
	if errors.As(err, &coded) {
		return coded.code
	}
	return classify(err)
}

func classify(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	switch {
	case errors.Is(err, audio.ErrUnavailable):
		return "pipewire_unavailable"
	case strings.Contains(msg, "focus_changed"):
		return "focus_changed"
	case strings.Contains(msg, "wtype"):
		return "wtype_failed"
	case strings.Contains(msg, "SWAYSOCK"), strings.Contains(msg, "sway"):
		return "sway_unavailable"
	default:
		return "runtime_error"
	}
}
