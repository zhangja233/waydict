package app

import (
	"context"
	"testing"
	"time"

	"waydict/internal/asr"
	"waydict/internal/audio"
	"waydict/internal/config"
	"waydict/pkg/api"
)

func TestHoldBuffersUntilRelease(t *testing.T) {
	app, mem, cleanup := startDeferredTestApp(t, api.ModeHold, &FakeEngine{Text: "hello.", IsLoaded: true})
	defer cleanup()

	app.queueSegment(asr.AudioSegment{ID: "first", Duration: time.Second})
	waitForBufferedParts(t, app, 1)
	if len(mem.Texts()) != 0 {
		t.Fatalf("text injected before release: %q", mem.Texts())
	}

	if err := app.Release(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForInjectedTexts(t, mem, 1)
	if mem.Texts()[0] != "Hello. " {
		t.Fatalf("injected text = %q", mem.Texts()[0])
	}
	waitForState(t, app, api.StateIdle)
}

func TestToggleBuffersUntilSecondToggle(t *testing.T) {
	app, mem, cleanup := startDeferredTestApp(t, api.ModeToggle, &FakeEngine{Text: "hello.", IsLoaded: true})
	defer cleanup()

	app.queueSegment(asr.AudioSegment{ID: "first", Duration: time.Second})
	waitForBufferedParts(t, app, 1)
	if len(mem.Texts()) != 0 {
		t.Fatalf("text injected before final toggle: %q", mem.Texts())
	}

	if err := app.Toggle(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForInjectedTexts(t, mem, 1)
	if mem.Texts()[0] != "Hello. " {
		t.Fatalf("injected text = %q", mem.Texts()[0])
	}
}

func TestReleaseDoesNotFinalizeToggle(t *testing.T) {
	app, mem, cleanup := startDeferredTestApp(t, api.ModeToggle, &FakeEngine{Text: "hello.", IsLoaded: true})
	defer cleanup()

	app.queueSegment(asr.AudioSegment{ID: "first", Duration: time.Second})
	waitForBufferedParts(t, app, 1)
	if err := app.Release(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(mem.Texts()) != 0 {
		t.Fatalf("hold release finalized toggle session: %q", mem.Texts())
	}
	if got := app.Status(context.Background()).Mode; got == nil || *got != api.ModeToggle {
		t.Fatalf("mode after release = %v, want toggle", got)
	}

	if err := app.Toggle(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForInjectedTexts(t, mem, 1)
}

func TestDeferredSegmentsInjectOnceInOrder(t *testing.T) {
	engine := &sequenceEngine{texts: []string{"First fragment", "Jumped over."}}
	app, mem, cleanup := startDeferredTestApp(t, api.ModeToggle, engine)
	defer cleanup()

	app.queueSegment(asr.AudioSegment{ID: "first", Duration: time.Second})
	app.queueSegment(asr.AudioSegment{ID: "second", Duration: time.Second})
	waitForBufferedParts(t, app, 2)
	if err := app.Toggle(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForInjectedTexts(t, mem, 1)
	if mem.Texts()[0] != "first fragment jumped over. " {
		t.Fatalf("injected text = %q", mem.Texts()[0])
	}
}

func TestHoldReleaseWaitsForPendingRecognition(t *testing.T) {
	releaseDecode := make(chan struct{})
	engine := &gateEngine{
		text:    "pending.",
		started: make(chan struct{}, 1),
		release: releaseDecode,
		done:    make(chan struct{}),
	}
	app, mem, cleanup := startDeferredTestApp(t, api.ModeHold, engine)
	defer cleanup()

	app.queueSegment(asr.AudioSegment{ID: "pending", Duration: time.Second})
	select {
	case <-engine.started:
	case <-time.After(time.Second):
		t.Fatal("recognition did not start")
	}
	if err := app.Release(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(mem.Texts()) != 0 {
		t.Fatalf("text injected before recognition completed: %q", mem.Texts())
	}
	close(releaseDecode)
	waitForInjectedTexts(t, mem, 1)
	if mem.Texts()[0] != "Pending. " {
		t.Fatalf("injected text = %q", mem.Texts()[0])
	}
}

func startDeferredTestApp(t *testing.T, mode api.Mode, engine asr.Engine) (*App, *MemoryInjector, func()) {
	t.Helper()
	cfg := config.Defaults()
	cfg.Daemon.AutoStopAfterSilenceSeconds = 0
	ctx, cancel := context.WithCancel(context.Background())
	mem := &MemoryInjector{}
	app := New(ctx, cfg, Dependencies{
		Source:   &audio.ScriptedSource{SampleRate: 16000, Delay: time.Millisecond},
		Engine:   engine,
		Injector: mem,
	})
	if err := app.Start(ctx, mode); err != nil {
		cancel()
		t.Fatal(err)
	}
	return app, mem, func() {
		_ = app.Stop(context.Background(), false)
		cancel()
	}
}

func waitForBufferedParts(t *testing.T, app *App, count int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		app.mu.Lock()
		deferred := app.deferred[app.currentSession]
		got := 0
		if deferred != nil {
			got = len(deferred.parts)
		}
		app.mu.Unlock()
		if got == count {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("did not buffer %d transcript parts", count)
}

func waitForInjectedTexts(t *testing.T, mem *MemoryInjector, count int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(mem.Texts()) == count {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("injected texts = %q, want %d entries", mem.Texts(), count)
}

func waitForState(t *testing.T, app *App, state api.State) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if app.Status(context.Background()).State == state {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("state = %s, want %s", app.Status(context.Background()).State, state)
}

type sequenceEngine struct {
	FakeEngine
	texts []string
	next  int
}

func (e *sequenceEngine) Transcribe(_ context.Context, seg asr.AudioSegment) (asr.Transcript, error) {
	text := e.texts[e.next]
	e.next++
	return asr.Transcript{
		SegmentID:     seg.ID,
		Text:          text,
		AudioDuration: seg.Duration,
		Empty:         text == "",
	}, nil
}
