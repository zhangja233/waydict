package exitcode

const (
	Success             = 0
	Generic             = 1
	Usage               = 2
	DaemonUnavailable   = 3
	DependencyMissing   = 4
	ModelInvalid        = 5
	PipeWireUnavailable = 6
	SwayUnavailable     = 7
	WtypeUnavailable    = 8
	RecognitionFailed   = 9
	Permission          = 10
)

func ForErrorCode(code string) int {
	switch code {
	case "":
		return Success
	case "daemon_unavailable":
		return DaemonUnavailable
	case "dependency_missing":
		return DependencyMissing
	case "model_invalid", "model_missing":
		return ModelInvalid
	case "pipewire_unavailable":
		return PipeWireUnavailable
	case "sway_unavailable", "focus_changed":
		return SwayUnavailable
	case "wtype_unavailable", "wtype_failed":
		return WtypeUnavailable
	case "recognition_timeout", "recognition_failed":
		return RecognitionFailed
	case "permission_denied", "socket_permission":
		return Permission
	case "usage":
		return Usage
	default:
		return Generic
	}
}
