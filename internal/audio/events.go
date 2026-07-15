package audio

import "time"

type EventKind string

const (
	EventDefaultChanged EventKind = "default_changed"
	EventDeviceRemoved  EventKind = "device_removed"
	EventDeviceAdded    EventKind = "device_added"
	EventFormatChanged  EventKind = "format_changed"
	EventOverrun        EventKind = "overrun"
)

type Event struct {
	Kind     EventKind
	DeviceID string
	At       time.Time
	Err      error
}

type EventSource interface {
	Events() <-chan Event
}
