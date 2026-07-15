package permissions

import (
	"testing"
	"time"
)

func TestStateValues(t *testing.T) {
	tests := []struct {
		state State
		want  string
	}{
		{NotDetermined, "not_determined"},
		{NotGranted, "not_granted"},
		{Granted, "granted"},
		{Denied, "denied"},
		{Restricted, "restricted"},
		{Unavailable, "unavailable"},
	}
	for _, test := range tests {
		if got := string(test.state); got != test.want {
			t.Errorf("state = %q, want %q", got, test.want)
		}
	}
}

func TestSnapshotState(t *testing.T) {
	snapshot := Snapshot{
		Microphone:      NotDetermined,
		Accessibility:   NotGranted,
		InputMonitoring: Granted,
	}
	tests := []struct {
		kind Kind
		want State
		ok   bool
	}{
		{KindMicrophone, NotDetermined, true},
		{KindAccessibility, NotGranted, true},
		{KindInputMonitoring, Granted, true},
		{Kind("other"), Unavailable, false},
	}
	for _, test := range tests {
		got, ok := snapshot.State(test.kind)
		if got != test.want || ok != test.ok {
			t.Errorf("State(%q) = (%q, %t), want (%q, %t)", test.kind, got, ok, test.want, test.ok)
		}
	}
}

func TestUnavailableSnapshot(t *testing.T) {
	checkedAt := time.Unix(123, 0)
	snapshot := UnavailableSnapshot(checkedAt)
	if snapshot.Microphone != Unavailable || snapshot.Accessibility != Unavailable || snapshot.InputMonitoring != Unavailable {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if !snapshot.CheckedAt.Equal(checkedAt) {
		t.Fatalf("CheckedAt = %v, want %v", snapshot.CheckedAt, checkedAt)
	}
}
