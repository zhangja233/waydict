package permissions

import permissionmodel "waydict/internal/permissions"

const (
	nativeStateNotDetermined = iota
	nativeStateNotGranted
	nativeStateGranted
	nativeStateDenied
	nativeStateRestricted
	nativeStateUnavailable
)

const (
	nativeKindMicrophone = iota
	nativeKindAccessibility
	nativeKindInputMonitoring
)

const (
	nativeResultOK = iota
	nativeResultInvalidKind
	nativeResultOpenSettingsFailed
)

func stateFromNative(state int) permissionmodel.State {
	switch state {
	case nativeStateNotDetermined:
		return permissionmodel.NotDetermined
	case nativeStateNotGranted:
		return permissionmodel.NotGranted
	case nativeStateGranted:
		return permissionmodel.Granted
	case nativeStateDenied:
		return permissionmodel.Denied
	case nativeStateRestricted:
		return permissionmodel.Restricted
	default:
		return permissionmodel.Unavailable
	}
}

func kindToNative(kind permissionmodel.Kind) (int, bool) {
	switch kind {
	case permissionmodel.KindMicrophone:
		return nativeKindMicrophone, true
	case permissionmodel.KindAccessibility:
		return nativeKindAccessibility, true
	case permissionmodel.KindInputMonitoring:
		return nativeKindInputMonitoring, true
	default:
		return 0, false
	}
}
