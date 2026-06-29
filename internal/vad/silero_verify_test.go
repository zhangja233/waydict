//go:build sherpa && cgo

package vad

import (
	"os"
	"testing"
	"time"

	"sway-voice/internal/asr"
	"sway-voice/internal/audio"
	"sway-voice/internal/config"
)

// TestSileroVerifyWAV runs the configured silero VAD over a real recording so
// the engine can be checked end-to-end on hardware audio. It is skipped unless
// SWAY_VOICE_VERIFY_WAV points at a speech file; override the model location
// with SWAY_VOICE_VERIFY_MODEL.
func TestSileroVerifyWAV(t *testing.T) {
	wavPath := os.Getenv("SWAY_VOICE_VERIFY_WAV")
	if wavPath == "" {
		t.Skip("set SWAY_VOICE_VERIFY_WAV to a speech recording to run")
	}
	clip, err := audio.ReadFile(wavPath)
	if err != nil {
		t.Fatalf("read %s: %v", wavPath, err)
	}
	cfg := config.Defaults().VAD
	cfg.Engine = "silero"
	if m := os.Getenv("SWAY_VOICE_VERIFY_MODEL"); m != "" {
		cfg.Model = m
	}
	if _, err := os.Stat(cfg.Model); err != nil {
		t.Fatalf("silero model not found at %s: %v", cfg.Model, err)
	}
	seg := NewSegmenter(cfg, clip.SampleRate)
	if _, ok := seg.(*SileroSegmenter); !ok {
		t.Fatalf("expected silero segmenter, got %T (model missing or failed to load)", seg)
	}
	now := time.Now()
	chunk := clip.SampleRate * 20 / 1000
	if chunk == 0 {
		chunk = 320
	}
	var segs []asr.AudioSegment
	for off := 0; off < len(clip.Samples); off += chunk {
		end := off + chunk
		if end > len(clip.Samples) {
			end = len(clip.Samples)
		}
		segs = append(segs, seg.Feed(clip.Samples[off:end], now)...)
	}
	segs = append(segs, seg.Flush(true, now)...)
	var total time.Duration
	for _, s := range segs {
		total += s.Duration
	}
	t.Logf("clip=%s dur=%s -> %d segment(s) total=%s", wavPath, clip.Duration, len(segs), total)
	if len(segs) == 0 {
		t.Fatalf("silero VAD produced no segments from a %s speech clip", clip.Duration)
	}
}
