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
