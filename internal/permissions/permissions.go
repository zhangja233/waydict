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
	NotDetermined State = "not_determined"
	NotGranted    State = "not_granted"
	Granted       State = "granted"
	Denied        State = "denied"
	Restricted    State = "restricted"
	Unavailable   State = "unavailable"
)

const (
	StateNotDetermined = NotDetermined
	StateNotGranted    = NotGranted
	StateGranted       = Granted
	StateDenied        = Denied
	StateRestricted    = Restricted
	StateUnavailable   = Unavailable
)

type Snapshot struct {
	Microphone      State
	Accessibility   State
	InputMonitoring State
	CheckedAt       time.Time
}

func (s Snapshot) State(kind Kind) (State, bool) {
	switch kind {
	case KindMicrophone:
		return s.Microphone, true
	case KindAccessibility:
		return s.Accessibility, true
	case KindInputMonitoring:
		return s.InputMonitoring, true
	default:
		return Unavailable, false
	}
}

func UnavailableSnapshot(checkedAt time.Time) Snapshot {
	return Snapshot{
		Microphone:      Unavailable,
		Accessibility:   Unavailable,
		InputMonitoring: Unavailable,
		CheckedAt:       checkedAt,
	}
}

type Source interface {
	Snapshot(context.Context) (Snapshot, error)
	Request(context.Context, Kind) (State, error)
	OpenSettings(context.Context, Kind) error
}
