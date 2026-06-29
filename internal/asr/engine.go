package asr

import (
	"context"
	"time"
)

type Engine interface {
	Name() string
	Load(ctx context.Context) error
	Close() error
	Transcribe(ctx context.Context, segment AudioSegment) (Transcript, error)
	Loaded() bool
}

type AudioSegment struct {
	ID             string
	Samples        []float32
	SampleRate     int
	StartedAt      time.Time
	Duration       time.Duration
	Degraded       bool
	CaptureOverrun bool
}

type Transcript struct {
	SegmentID       string
	Text            string
	Tokens          []string
	TokenTimestamps []float64
	StartedAt       time.Time
	AudioDuration   time.Duration
	DecodeDuration  time.Duration
	RealTimeFactor  float64
	Empty           bool
}
