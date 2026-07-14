package app

import (
	"strings"

	"waydict/internal/inject"
	"waydict/internal/swayipc"
	"waydict/pkg/api"
)

type deferredSession struct {
	mode            api.Mode
	parts           []string
	caseState       inject.CaseState
	commitRequested bool
	finalizing      bool
}

func deferredMode(mode api.Mode) bool {
	return mode == api.ModeHold || mode == api.ModeToggle
}

func modeName(mode *api.Mode) string {
	if mode == nil {
		return "unknown"
	}
	return string(*mode)
}

func (a *App) bufferDeferredTranscript(session uint64, transcript string) bool {
	a.mu.Lock()
	deferred := a.deferred[session]
	if deferred == nil {
		a.mu.Unlock()
		return false
	}
	if _, discarded := a.discarded[session]; discarded {
		a.mu.Unlock()
		return true
	}
	post := a.post
	caseState := deferred.caseState
	a.mu.Unlock()

	text, next := post.Apply(transcript, caseState)
	if text == "" {
		return true
	}

	a.mu.Lock()
	deferred = a.deferred[session]
	if deferred == nil {
		a.mu.Unlock()
		return true
	}
	if _, discarded := a.discarded[session]; discarded {
		a.mu.Unlock()
		return true
	}
	deferred.parts = append(deferred.parts, text)
	deferred.caseState = next
	a.mu.Unlock()
	a.logDebug("transcript buffered", "session", session, "text_bytes", len(text))
	return true
}

func (a *App) beginDeferredFinalizeLocked(session uint64) bool {
	deferred := a.deferred[session]
	if deferred == nil || !deferred.commitRequested || deferred.finalizing || a.pendingSession[session] > 0 {
		return false
	}
	if _, discarded := a.discarded[session]; discarded {
		return false
	}
	deferred.finalizing = true
	if session == a.currentSession {
		a.status.State = api.StateTyping
	}
	return true
}

func (a *App) finalizeDeferred(session uint64) {
	a.mu.Lock()
	deferred := a.deferred[session]
	if deferred == nil {
		a.mu.Unlock()
		return
	}
	text := strings.Join(deferred.parts, "")
	focusCheck := a.cfg.Sway.FocusCheck
	redacted := a.cfg.Daemon.RedactTranscriptsInLogs
	a.mu.Unlock()

	if text == "" || a.sessionDiscarded(session) {
		a.finishDeferred(session)
		return
	}

	ctx := a.rootCtx
	var focusWarning *swayipc.FocusChange
	if a.guard != nil && focusCheck {
		var err error
		focusWarning, err = a.guard.CheckWithWarning(ctx)
		if err != nil {
			a.logWarn("focus guard cancelled deferred injection", "session", session, "error", err)
			a.recordCanceledTranscript(text, err)
			a.finishDeferred(session)
			return
		}
	}
	if a.sessionDiscarded(session) {
		a.finishDeferred(session)
		return
	}
	if a.injector != nil {
		a.logDebug("typing deferred transcript", "session", session, "text_bytes", len(text), "redacted", redacted)
		if err := a.injector.TypeText(ctx, text); err != nil {
			a.recordUninjected(text, err)
			a.finishDeferred(session)
			return
		}
	}
	a.recordTranscript(text)
	if focusWarning != nil {
		a.recordWarning("focus_changed", focusWarning.Error())
	}
	a.finishDeferred(session)
}

func (a *App) finishDeferred(session uint64) {
	a.mu.Lock()
	delete(a.deferred, session)
	delete(a.discarded, session)
	if session == a.currentSession && !a.capturing {
		a.status.State = api.StateIdle
		a.status.Mode = nil
		a.segmentOpen = false
	}
	a.mu.Unlock()
}
