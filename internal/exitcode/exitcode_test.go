package exitcode

import "testing"

func TestForErrorCode(t *testing.T) {
	tests := map[string]int{
		"":                     Success,
		"daemon_unavailable":   DaemonUnavailable,
		"model_invalid":        ModelInvalid,
		"pipewire_unavailable": PipeWireUnavailable,
		"sway_unavailable":     SwayUnavailable,
		"wtype_failed":         WtypeUnavailable,
		"recognition_timeout":  RecognitionFailed,
		"socket_permission":    Permission,
		"usage":                Usage,
		"other":                Generic,
	}
	for code, want := range tests {
		if got := ForErrorCode(code); got != want {
			t.Fatalf("%q => %d, want %d", code, got, want)
		}
	}
}
