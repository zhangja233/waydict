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

type Accelerator struct {
	Provider string
	Device   int
	Name     string
}

// Served names which side decoded a segment on an engine that can offload.
const (
	ServedRemote   = "remote"
	ServedFallback = "fallback"
)

// RemoteStatus reports how the last segment was decoded, so status output and
// logs can distinguish a peer's GPU from a silent drop to the local CPU.
type RemoteStatus struct {
	Socket    string
	Served    string // ServedRemote or ServedFallback; empty before the first segment
	LastError string // why the last remote attempt failed, if it did
	LastRTF   float64
	Fallback  string // name of the local engine standing by, "" when there is none
}

// RemoteReporter is implemented by engines that decode off-host.
type RemoteReporter interface {
	RemoteStatus() RemoteStatus
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
