package vad

import (
	"time"

	"sway-voice/internal/asr"
)

type Segmenter interface {
	Feed(samples []float32, now time.Time) []asr.AudioSegment
	Flush(commit bool, now time.Time) []asr.AudioSegment
	Reset()
}
