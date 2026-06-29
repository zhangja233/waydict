package vad

import (
	"testing"
	"time"

	"sway-voice/internal/config"
)

func TestEnergySegmentBoundary(t *testing.T) {
	cfg := config.Defaults().VAD
	cfg.Threshold = 0.01
	cfg.MinSpeechMS = 20
	cfg.MinSilenceMS = 20
	cfg.SpeechPadMS = 0
	cfg.PreRollMS = 0
	cfg.MaxSpeechSeconds = 3
	s := NewEnergySegmenter(cfg, 16000)
	chunk := func(v float32) []float32 {
		out := make([]float32, 160)
		for i := range out {
			out[i] = v
		}
		return out
	}
	now := time.Now()
	for i := 0; i < 35; i++ {
		if segs := s.Feed(chunk(0.1), now); len(segs) != 0 {
			t.Fatalf("unexpected segment: %v", segs)
		}
	}
	if !s.SegmentOpen() {
		t.Fatal("segment was not marked open during speech")
	}
	if segs := s.Feed(chunk(0), now); len(segs) != 0 {
		t.Fatalf("unexpected segment before silence threshold: %v", segs)
	}
	segs := s.Feed(chunk(0), now)
	if len(segs) != 1 {
		t.Fatalf("segments = %d", len(segs))
	}
	if s.SegmentOpen() {
		t.Fatal("segment remained open after boundary")
	}
	if segs[0].Duration <= 0 || len(segs[0].Samples) == 0 {
		t.Fatalf("bad segment: %+v", segs[0])
	}
}

func TestEnergyDropsShortEndpointedSegment(t *testing.T) {
	cfg := testEnergyConfig()
	s := NewEnergySegmenter(cfg, 16000)
	now := time.Now()
	for i := 0; i < 5; i++ {
		if segs := s.Feed(testChunk(0.1), now); len(segs) != 0 {
			t.Fatalf("unexpected speech segment: %v", segs)
		}
	}
	if !s.SegmentOpen() {
		t.Fatal("segment was not open before endpoint")
	}
	if segs := s.Feed(testChunk(0), now); len(segs) != 0 {
		t.Fatalf("unexpected segment before endpoint: %v", segs)
	}
	if segs := s.Feed(testChunk(0), now); len(segs) != 0 {
		t.Fatalf("short endpointed segment was not dropped: %v", segs)
	}
	if s.SegmentOpen() {
		t.Fatal("short endpointed segment remained open")
	}
}

func TestEnergyCommitKeepsShortOpenSegment(t *testing.T) {
	cfg := testEnergyConfig()
	s := NewEnergySegmenter(cfg, 16000)
	now := time.Now()
	for i := 0; i < 5; i++ {
		if segs := s.Feed(testChunk(0.1), now); len(segs) != 0 {
			t.Fatalf("unexpected speech segment: %v", segs)
		}
	}
	segs := s.Flush(true, now)
	if len(segs) != 1 {
		t.Fatalf("segments = %d, want 1", len(segs))
	}
	if segs[0].Duration >= 300*time.Millisecond {
		t.Fatalf("segment duration = %s, want shorter than 300ms", segs[0].Duration)
	}
	if len(segs[0].Samples) == 0 {
		t.Fatal("committed segment had no samples")
	}
	if s.SegmentOpen() {
		t.Fatal("segment remained open after commit")
	}
}

func TestEnergyHysteresisKeepsSegmentOpenThroughDip(t *testing.T) {
	cfg := config.Defaults().VAD
	cfg.Engine = "energy"
	cfg.Threshold = 0.06         // open bar
	cfg.NegativeThreshold = 0.02 // close bar
	cfg.MinSpeechMS = 20
	cfg.MinSilenceMS = 100
	cfg.SpeechPadMS = 0
	cfg.PreRollMS = 0
	cfg.MaxSpeechSeconds = 5
	s := NewEnergySegmenter(cfg, 16000)
	now := time.Now()
	for i := 0; i < 6; i++ {
		if segs := s.Feed(testChunk(0.10), now); len(segs) != 0 {
			t.Fatalf("unexpected segment while opening: %v", segs)
		}
	}
	if !s.SegmentOpen() {
		t.Fatal("segment did not open on loud speech")
	}
	// A level between the close (0.02) and open (0.06) bars must NOT end the
	// segment; with the old single-threshold logic this dip closed it.
	for i := 0; i < 30; i++ {
		if segs := s.Feed(testChunk(0.04), now); len(segs) != 0 {
			t.Fatalf("segment closed during mid-level dip (hysteresis broken): %v", segs)
		}
	}
	if !s.SegmentOpen() {
		t.Fatal("segment closed during a dip above the close threshold")
	}
	// Dropping below the close bar must end the segment after min silence.
	closed := false
	for i := 0; i < 30; i++ {
		if segs := s.Feed(testChunk(0.0), now); len(segs) == 1 {
			closed = true
			break
		}
	}
	if !closed {
		t.Fatal("segment never closed after dropping below the close threshold")
	}
}

func testEnergyConfig() config.VAD {
	cfg := config.Defaults().VAD
	cfg.Threshold = 0.01
	cfg.MinSpeechMS = 20
	cfg.MinSilenceMS = 20
	cfg.SpeechPadMS = 0
	cfg.PreRollMS = 0
	cfg.MaxSpeechSeconds = 3
	return cfg
}

func testChunk(v float32) []float32 {
	out := make([]float32, 160)
	for i := range out {
		out[i] = v
	}
	return out
}
