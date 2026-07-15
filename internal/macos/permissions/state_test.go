package permissions

import (
	"testing"

	permissionmodel "waydict/internal/permissions"
)

func TestStateFromNative(t *testing.T) {
	tests := []struct {
		native int
		want   permissionmodel.State
	}{
		{nativeStateNotDetermined, permissionmodel.NotDetermined},
		{nativeStateNotGranted, permissionmodel.NotGranted},
		{nativeStateGranted, permissionmodel.Granted},
		{nativeStateDenied, permissionmodel.Denied},
		{nativeStateRestricted, permissionmodel.Restricted},
		{nativeStateUnavailable, permissionmodel.Unavailable},
		{99, permissionmodel.Unavailable},
	}
	for _, test := range tests {
		if got := stateFromNative(test.native); got != test.want {
			t.Errorf("stateFromNative(%d) = %q, want %q", test.native, got, test.want)
		}
	}
}

func TestKindToNative(t *testing.T) {
	tests := []struct {
		kind permissionmodel.Kind
		want int
		ok   bool
	}{
		{permissionmodel.KindMicrophone, nativeKindMicrophone, true},
		{permissionmodel.KindAccessibility, nativeKindAccessibility, true},
		{permissionmodel.KindInputMonitoring, nativeKindInputMonitoring, true},
		{permissionmodel.Kind("other"), 0, false},
	}
	for _, test := range tests {
		got, ok := kindToNative(test.kind)
		if got != test.want || ok != test.ok {
			t.Errorf("kindToNative(%q) = (%d, %t), want (%d, %t)", test.kind, got, ok, test.want, test.ok)
		}
	}
}
