package hotkey

import (
	"context"
	"time"
)

type Mode string

const (
	ModeHold    Mode = "hold"
	ModeToggle  Mode = "toggle"
	ModeOneshot Mode = "oneshot"
)

type Modifiers uint32

type Binding struct {
	Key       string
	KeyCode   uint16
	Modifiers Modifiers
	Mode      Mode
}

type Action string

const (
	Press   Action = "press"
	Release Action = "release"
	Abort   Action = "abort"
)

type Event struct {
	Action Action
	At     time.Time
}

type Handler func(Event)

type Status struct {
	Running       bool
	Binding       Binding
	DisableCount  int
	LastErrorCode string
}

type Service interface {
	Available(context.Context) error
	Start(context.Context, Binding, Handler) error
	Rebind(context.Context, Binding) error
	Stop(context.Context) error
	Status() Status
}
