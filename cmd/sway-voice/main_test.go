package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"testing"

	"sway-voice/internal/asr"
	"sway-voice/internal/config"
	"sway-voice/internal/exitcode"
	"sway-voice/internal/inject"
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

func TestTranscribeInjectCapturesFocusBeforeDecode(t *testing.T) {
	restore := replaceTranscribeDeps(t)
	defer restore()
	guard := &fakeFocusGuard{}
	typed := ""
	newFocusGuard = func(config.Config) focusGuard { return guard }
	newInjector = func(config.Injection) inject.Injector {
		return fakeInjector{typeText: func(_ context.Context, text string) error {
			if !guard.checked {
				t.Fatal("focus was not checked before injection")
			}
			typed = text
			return nil
		}}
	}
	transcribeFileFunc = func(_ context.Context, _ config.Config, _ string, _ io.Writer) (asr.Transcript, int) {
		if !guard.captured {
			t.Fatal("focus was not captured before decode")
		}
		return asr.Transcript{Text: "hello"}, exitcode.Success
	}
	var out, err bytes.Buffer
	if got := run([]string{"transcribe", "--file", "sample.wav", "--inject"}, &out, &err); got != exitcode.Success {
		t.Fatalf("exit = %d, want %d; stdout=%s stderr=%s", got, exitcode.Success, out.String(), err.String())
	}
	if typed != "hello " {
		t.Fatalf("typed = %q", typed)
	}
}

func TestTranscribeInjectFocusChangePreventsType(t *testing.T) {
	restore := replaceTranscribeDeps(t)
	defer restore()
	guard := &fakeFocusGuard{checkErr: fmt.Errorf("focus_changed: focus changed from 1 to 2")}
	typed := false
	newFocusGuard = func(config.Config) focusGuard { return guard }
	newInjector = func(config.Injection) inject.Injector {
		return fakeInjector{typeText: func(context.Context, string) error {
			typed = true
			return nil
		}}
	}
	transcribeFileFunc = func(context.Context, config.Config, string, io.Writer) (asr.Transcript, int) {
		return asr.Transcript{Text: "secret"}, exitcode.Success
	}
	var out, err bytes.Buffer
	if got := run([]string{"transcribe", "--file", "sample.wav", "--inject"}, &out, &err); got != exitcode.SwayUnavailable {
		t.Fatalf("exit = %d, want %d; stdout=%s stderr=%s", got, exitcode.SwayUnavailable, out.String(), err.String())
	}
	if typed {
		t.Fatal("text was typed after focus change")
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

func replaceTranscribeDeps(t *testing.T) func() {
	t.Helper()
	oldTranscribe := transcribeFileFunc
	oldInjector := newInjector
	oldGuard := newFocusGuard
	return func() {
		transcribeFileFunc = oldTranscribe
		newInjector = oldInjector
		newFocusGuard = oldGuard
	}
}

type fakeFocusGuard struct {
	captured bool
	checked  bool
	checkErr error
}

func (f *fakeFocusGuard) CaptureStart(context.Context) error {
	f.captured = true
	return nil
}

func (f *fakeFocusGuard) Check(context.Context) error {
	f.checked = true
	return f.checkErr
}

type fakeInjector struct {
	typeText func(context.Context, string) error
}

func (f fakeInjector) Available(context.Context) error {
	return nil
}

func (f fakeInjector) TypeText(ctx context.Context, text string) error {
	if f.typeText == nil {
		return nil
	}
	return f.typeText(ctx, text)
}
