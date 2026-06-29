package vad

import (
	"time"

	"waydict/internal/asr"
)

type Segmenter interface {
	Feed(samples []float32, now time.Time) []asr.AudioSegment
	Flush(commit bool, now time.Time) []asr.AudioSegment
	Reset()
	// Name reports the active engine ("silero" or "energy") for status and diagnostics.
	Name() string
}
