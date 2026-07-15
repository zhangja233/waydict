package apperr

import (
	"errors"
	"fmt"
	"testing"
)

func TestErrorWrapUnwrapAndCode(t *testing.T) {
	cause := errors.New("denied")
	err := New(CodePermissionMicrophoneDenied, "start capture", cause)
	if got, want := err.Error(), "start capture: denied"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
	if !errors.Is(err, cause) {
		t.Fatal("wrapped cause is not discoverable")
	}
	wrapped := fmt.Errorf("outer: %w", err)
	if got := Code(wrapped); got != CodePermissionMicrophoneDenied {
		t.Fatalf("Code() = %q", got)
	}
}

func TestCodeUnknownIsInternalError(t *testing.T) {
	if got := Code(errors.New("unknown")); got != CodeInternalError {
		t.Fatalf("Code() = %q, want %q", got, CodeInternalError)
	}
	if got := Code(nil); got != "" {
		t.Fatalf("Code(nil) = %q", got)
	}
}
