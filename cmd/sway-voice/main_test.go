package main

import (
	"bytes"
	"path/filepath"
	"testing"

	"sway-voice/internal/exitcode"
)

func TestRunUsage(t *testing.T) {
	var out, err bytes.Buffer
	if got := run(nil, &out, &err); got != exitcode.Usage {
		t.Fatalf("exit = %d, want %d", got, exitcode.Usage)
	}
}

func TestStatusDaemonUnavailable(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	var out, err bytes.Buffer
	if got := run([]string{"status", "--json"}, &out, &err); got != exitcode.DaemonUnavailable {
		t.Fatalf("exit = %d, want %d; stderr=%s", got, exitcode.DaemonUnavailable, err.String())
	}
	if out.Len() != 0 {
		t.Fatalf("unexpected stdout: %s", out.String())
	}
}

func TestStartRejectsInvalidModeBeforeDial(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	var out, err bytes.Buffer
	if got := run([]string{"start", "--mode", "streaming"}, &out, &err); got != exitcode.Usage {
		t.Fatalf("exit = %d, want %d; stdout=%s stderr=%s", got, exitcode.Usage, out.String(), err.String())
	}
}

func TestStopRejectsCommitDiscardConflict(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	var out, err bytes.Buffer
	if got := run([]string{"stop", "--commit", "--discard"}, &out, &err); got != exitcode.Usage {
		t.Fatalf("exit = %d, want %d; stdout=%s stderr=%s", got, exitcode.Usage, out.String(), err.String())
	}
}

func TestModelCheckMissingDirectory(t *testing.T) {
	var out, err bytes.Buffer
	dir := filepath.Join(t.TempDir(), "missing")
	if got := run([]string{"model", "check", "--dir", dir}, &out, &err); got != exitcode.ModelInvalid {
		t.Fatalf("exit = %d, want %d; stdout=%s stderr=%s", got, exitcode.ModelInvalid, out.String(), err.String())
	}
	if out.Len() == 0 {
		t.Fatal("expected diagnostic output")
	}
}
