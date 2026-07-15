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

const (
	StateUnknown       State = "unknown"
	StateNotDetermined State = "not_determined"
	StateDenied        State = "denied"
	StateGranted       State = "granted"
	StateRestricted    State = "restricted"
)

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
