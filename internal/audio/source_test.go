package audio

import (
	"strings"
	"testing"

	"waydict/internal/apperr"
)

func TestUnavailableIsGenericTypedError(t *testing.T) {
	if got := apperr.Code(ErrUnavailable); got != apperr.CodeAudioBackendUnavailable {
		t.Fatalf("code = %q", got)
	}
	if strings.Contains(strings.ToLower(ErrUnavailable.Error()), "pipewire") {
		t.Fatalf("unavailable error is backend-specific: %v", ErrUnavailable)
	}
}
