package permissions

import (
	"context"
	"time"
)

type Kind string

const (
	KindMicrophone      Kind = "microphone"
	KindAccessibility   Kind = "accessibility"
	KindInputMonitoring Kind = "input_monitoring"
)

type State string

type Snapshot struct {
	Microphone      State
	Accessibility   State
	InputMonitoring State
	CheckedAt       time.Time
}

type Source interface {
	Snapshot(context.Context) (Snapshot, error)
	Request(context.Context, Kind) (State, error)
	OpenSettings(context.Context, Kind) error
}
