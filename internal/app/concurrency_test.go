package app

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"waydict/internal/asr"
	"waydict/internal/audio"
	"waydict/internal/config"
	"waydict/pkg/api"
)

// raceCheckSegmenter flags if it is ever entered by two goroutines at once,
// which is what corrupted the silero VAD's C heap (SIGABRT) before the
// segmenter access was serialized.
type raceCheckSegmenter struct {
	active atomic.Int32
	raced  atomic.Bool
	feeds  atomic.Int64
}

func (s *raceCheckSegmenter) enter() {
	if s.active.Add(1) != 1 {
		s.raced.Store(true)
	}
	time.Sleep(30 * time.Microsecond) // widen the window
}

func (s *raceCheckSegmenter) leave() { s.active.Add(-1) }

func (s *raceCheckSegmenter) Feed(_ []float32, _ time.Time) []asr.AudioSegment {
	s.enter()
	defer s.leave()
	s.feeds.Add(1)
	return nil
}

func (s *raceCheckSegmenter) Flush(_ bool, _ time.Time) []asr.AudioSegment {
	s.enter()
	defer s.leave()
	return nil
}

func (s *raceCheckSegmenter) Reset() { s.enter(); s.leave() }

func (s *raceCheckSegmenter) Name() string { return "fake" }

// continuousSource always returns audio while capturing, so captureLoop keeps
// calling the segmenter and any cross-session overlap is exercised.
type continuousSource struct{ capturing atomic.Bool }

func (s *continuousSource) Start(context.Context) error { s.capturing.Store(true); return nil }
func (s *continuousSource) Pause(context.Context) error { s.capturing.Store(false); return nil }
func (s *continuousSource) Stop(context.Context) error  { s.capturing.Store(false); return nil }

func (s *continuousSource) Read(ctx context.Context, dst []float32) (int, error) {
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
	}
	if !s.capturing.Load() {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(time.Millisecond):
			return 0, nil
		}
	}
	for i := range dst {
		dst[i] = 0.2
	}
	return len(dst), nil
}

func (s *continuousSource) Stats() audio.Stats {
	return audio.Stats{SampleRate: 16000, Capturing: s.capturing.Load()}
}

// TestConcurrentStartStopNoSegmenterRace hammers Start/Stop from many goroutines
// and asserts the segmenter is never entered concurrently. Run with -race.
func TestConcurrentStartStopNoSegmenterRace(t *testing.T) {
	cfg := config.Defaults()
	cfg.Daemon.AutoStopAfterSilenceSeconds = 0
	cfg.Sway.RequireSway = false
	cfg.Sway.FocusCheck = false
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	seg := &raceCheckSegmenter{}
	app := New(ctx, cfg, Dependencies{
		Source:    &continuousSource{},
		Segmenter: seg,
		Engine:    &FakeEngine{IsLoaded: true},
		Injector:  &MemoryInjector{},
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 40; j++ {
				_ = app.Start(ctx, api.ModeToggle)
				time.Sleep(150 * time.Microsecond) // let captureLoop feed
				_ = app.Stop(ctx, true)
			}
		}()
	}
	wg.Wait()
	_ = app.Stop(ctx, false)

	if seg.raced.Load() {
		t.Fatal("segmenter was accessed concurrently")
	}
	if seg.feeds.Load() == 0 {
		t.Fatal("segmenter was never fed; test did not exercise the capture path")
	}
}
