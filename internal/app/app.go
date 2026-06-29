package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"sway-voice/internal/asr"
	"sway-voice/internal/audio"
	"sway-voice/internal/config"
	"sway-voice/internal/control"
	"sway-voice/internal/inject"
	"sway-voice/internal/swayipc"
	"sway-voice/internal/vad"
	"sway-voice/pkg/api"
)

type Dependencies struct {
	Source        audio.Source
	SourceFactory func() (audio.Source, error)
	ModelChecker  func() error
	Segmenter     vad.Segmenter
	Engine        asr.Engine
	Injector      inject.Injector
	Focus         *swayipc.Client
	Shutdown      func()
}

type App struct {
	cfg           config.Config
	source        audio.Source
	sourceFactory func() (audio.Source, error)
	modelChecker  func() error
	segmenter     vad.Segmenter
	engine        asr.Engine
	injector      inject.Injector
	guard         *swayipc.Guard
	focus         *swayipc.Client
	post          inject.PostProcessor

	mu             sync.Mutex
	status         api.Status
	startedAt      time.Time
	sessionCancel  context.CancelFunc
	capturing      bool
	rootCtx        context.Context
	asrQueue       chan segmentJob
	shutdown       func()
	lastActivity   time.Time
	retryingAudio  bool
	currentSession uint64
	discarded      map[uint64]struct{}
	pendingASR     int
}

type segmentJob struct {
	session uint64
	segment asr.AudioSegment
}

func New(ctx context.Context, cfg config.Config, deps Dependencies) *App {
	a := &App{
		cfg:           cfg,
		source:        deps.Source,
		sourceFactory: deps.SourceFactory,
		modelChecker:  deps.ModelChecker,
		segmenter:     deps.Segmenter,
		engine:        deps.Engine,
		injector:      deps.Injector,
		focus:         deps.Focus,
		post:          inject.NewPostProcessor(cfg.PostProcess, cfg.Injection.AppendSpace),
		startedAt:     time.Now(),
		rootCtx:       ctx,
		asrQueue:      make(chan segmentJob, 8),
		shutdown:      deps.Shutdown,
		discarded:     make(map[uint64]struct{}),
	}
	if deps.Focus != nil {
		a.guard = swayipc.NewGuard(deps.Focus, cfg.Injection.FocusPolicy)
	}
	a.status = api.Status{
		State: api.StateIdle,
		Audio: api.AudioStatus{
			Backend:    cfg.Audio.Backend,
			SampleRate: cfg.Audio.SampleRate,
			LevelDBFS:  -120,
		},
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
		a.mu.Unlock()
		return nil
	}
	a.status.State = api.StateArming
	a.status.Mode = modePtr(mode)
	a.status.LastError = nil
	a.currentSession++
	a.mu.Unlock()

	if a.injector != nil {
		if err := a.injector.Available(ctx); err != nil {
			a.recordError(api.StateIdle, "wtype_unavailable", err)
			return withCode("wtype_unavailable", err)
		}
	}
	if a.engine != nil && !a.engine.Loaded() {
		if a.modelChecker != nil {
			if err := a.modelChecker(); err != nil {
				a.recordError(api.StateIdle, "model_invalid", err)
				return withCode("model_invalid", err)
			}
		}
		if err := a.engine.Load(ctx); err != nil {
			a.recordError(api.StateIdle, "model_invalid", err)
			return withCode("model_invalid", err)
		}
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
		if err := a.recreateSource(ctx); err != nil {
			a.recordError(api.StateIdle, "pipewire_unavailable", err)
			return withCode("pipewire_unavailable", err)
		}
	}
	a.mu.Lock()
	src = a.source
	a.mu.Unlock()
	if src == nil {
		err := audio.ErrUnavailable
		a.recordError(api.StateIdle, "pipewire_unavailable", err)
		return withCode("pipewire_unavailable", err)
	}
	if err := src.Start(ctx); err != nil {
		a.clearSource(src)
		a.recordError(api.StateIdle, "pipewire_unavailable", err)
		return withCode("pipewire_unavailable", err)
	}
	sessionCtx, cancel := context.WithCancel(a.rootCtx)
	a.mu.Lock()
	a.sessionCancel = cancel
	a.capturing = true
	a.lastActivity = time.Now()
	a.status.State = api.StateListening
	a.status.Audio.Capturing = true
	a.status.ASR.Loaded = a.engine != nil && a.engine.Loaded()
	a.mu.Unlock()
	go a.captureLoop(sessionCtx)
	go a.autoStopLoop(sessionCtx)
	return nil
}

func (a *App) Stop(ctx context.Context, commit bool) error {
	a.mu.Lock()
	cancel := a.sessionCancel
	session := a.currentSession
	a.sessionCancel = nil
	wasCapturing := a.capturing
	a.capturing = false
	a.status.Audio.Capturing = false
	if !commit {
		a.discarded[session] = struct{}{}
	}
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	a.mu.Lock()
	src := a.source
	a.mu.Unlock()
	if src != nil && wasCapturing {
		if err := src.Pause(ctx); err != nil {
			a.recordError(api.StateIdle, "pipewire_unavailable", err)
			return withCode("pipewire_unavailable", err)
		}
	}
	if a.segmenter != nil {
		for _, seg := range a.segmenter.Flush(commit, time.Now()) {
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
	var prevOverruns uint64
	for {
		a.mu.Lock()
		src := a.source
		a.mu.Unlock()
		if src == nil {
			a.recordError(api.StateIdle, "pipewire_unavailable", audio.ErrUnavailable)
			return
		}
		n, err := src.Read(ctx, buf)
		if err != nil {
			if ctx.Err() == nil {
				if a.segmenter != nil {
					a.segmenter.Reset()
				}
				a.recordError(api.StateIdle, classify(err), err)
				a.scheduleAudioRetry()
			}
			return
		}
		if n == 0 {
			continue
		}
		if stats := src.Stats(); stats.Overruns > prevOverruns {
			prevOverruns = stats.Overruns
			if marker, ok := a.segmenter.(interface{ MarkCaptureOverrun() }); ok {
				marker.MarkCaptureOverrun()
			}
		}
		now := time.Now()
		if audio.LevelDBFS(buf[:n]) > -45 {
			a.touchActivity(now)
		}
		if a.segmenter == nil {
			continue
		}
		for _, seg := range a.segmenter.Feed(buf[:n], now) {
			a.queueSegment(seg)
		}
	}
}

func (a *App) queueSegment(seg asr.AudioSegment) {
	a.mu.Lock()
	session := a.currentSession
	if _, ok := a.discarded[session]; ok {
		a.mu.Unlock()
		return
	}
	a.status.State = api.StateRecognizing
	a.lastActivity = time.Now()
	a.pendingASR++
	a.mu.Unlock()
	job := segmentJob{session: session, segment: seg}
	select {
	case a.asrQueue <- job:
	case <-a.rootCtx.Done():
		a.finishASRJob(session)
	default:
		a.finishASRJob(session)
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
		return err
	}
	a.mu.Lock()
	old := a.source
	a.source = src
	a.status.Audio.Capturing = false
	a.status.Audio.SampleRate = src.Stats().SampleRate
	a.mu.Unlock()
	closeSource(old)
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
				return
			}
			if err := a.recreateSource(a.rootCtx); err == nil {
				a.mu.Lock()
				a.status.LastError = nil
				a.mu.Unlock()
				return
			}
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
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-a.asrQueue:
			if a.sessionDiscarded(job.session) {
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
	tr, err := a.engine.Transcribe(tctx, seg)
	cancel()
	if err != nil {
		if a.sessionDiscarded(job.session) {
			a.setState(a.nextState())
			return
		}
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
	if tr.Empty {
		a.setState(a.nextState())
		return
	}
	text := a.post.Apply(tr.Text)
	if text == "" {
		a.setState(a.nextState())
		return
	}
	if a.guard != nil && a.cfg.Sway.FocusCheck {
		if err := a.guard.Check(ctx); err != nil {
			a.recordCanceledTranscript(text, err)
			a.setState(a.nextState())
			return
		}
	}
	if a.injector != nil {
		a.setState(api.StateTyping)
		if err := a.injector.TypeText(ctx, text); err != nil {
			a.recordUninjected(text, err)
			a.setState(a.nextState())
			return
		}
	}
	a.recordTranscript(text)
	a.finishModeAfterSegment(ctx)
}

func (a *App) finishModeAfterSegment(ctx context.Context) {
	a.mu.Lock()
	mode := a.status.Mode
	a.mu.Unlock()
	if mode != nil && *mode == api.ModeOneshot {
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
	return audio.WriteWAVFloat32(path, seg.Samples, seg.SampleRate)
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
		return api.StateListening
	}
	return api.StateIdle
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
				a.status.State = api.StateListening
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
	}
}

func (a *App) recordError(state api.State, code string, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.status.State = state
	a.status.LastError = &api.ErrorInfo{Code: code, Message: err.Error()}
	if state == api.StateIdle || state == api.StateError {
		a.status.Mode = nil
		a.capturing = false
		a.status.Audio.Capturing = false
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
	a.status.LastUninjectedText = ""
	a.status.LastTranscriptRedacted = a.cfg.Daemon.RedactTranscriptsInLogs
	if a.cfg.Daemon.RedactTranscriptsInLogs {
		a.status.LastTranscript = ""
	} else {
		a.status.LastTranscript = text
	}
}

func (a *App) recordCanceledTranscript(text string, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.status.LastError = &api.ErrorInfo{Code: "focus_changed", Message: err.Error()}
	a.status.LastTranscriptRedacted = a.cfg.Daemon.RedactTranscriptsInLogs
	if !a.cfg.Daemon.RedactTranscriptsInLogs {
		a.status.LastUninjectedText = text
	} else {
		a.status.LastUninjectedText = ""
	}
}

func (a *App) recordUninjected(text string, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.status.LastError = &api.ErrorInfo{Code: "wtype_failed", Message: err.Error()}
	a.status.LastTranscriptRedacted = a.cfg.Daemon.RedactTranscriptsInLogs
	if !a.cfg.Daemon.RedactTranscriptsInLogs {
		a.status.LastUninjectedText = text
	} else {
		a.status.LastUninjectedText = ""
	}
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
