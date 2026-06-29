package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"sway-voice/internal/asr"
	"sway-voice/internal/audio"
	"sway-voice/internal/config"
	"sway-voice/internal/control"
	"sway-voice/internal/exitcode"
	"sway-voice/internal/inject"
	"sway-voice/internal/model"
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

func TestControlSocketPermissionExit(t *testing.T) {
	err := fmt.Errorf("%w: denied", control.ErrSocketPermission)
	if got := exitForControlErr(err); got != exitcode.Permission {
		t.Fatalf("exit = %d, want %d", got, exitcode.Permission)
	}
	if !errors.Is(err, control.ErrSocketPermission) {
		t.Fatal("wrapped socket permission error did not match sentinel")
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

func TestBenchRejectsEmptyAudio(t *testing.T) {
	restore := replaceTranscribeDeps(t)
	defer restore()
	validateModelForUseFn = func(config.Config) error { return nil }
	readAudioFileFunc = func(string) (audio.FileAudio, error) {
		return audio.FileAudio{}, nil
	}
	var out, err bytes.Buffer
	if got := run([]string{"bench", "--file", "empty.wav"}, &out, &err); got != exitcode.Generic {
		t.Fatalf("exit = %d, want %d; stdout=%s stderr=%s", got, exitcode.Generic, out.String(), err.String())
	}
}

func TestTranscribeRejectsInvalidASRConfig(t *testing.T) {
	restore := replaceTranscribeDeps(t)
	defer restore()
	transcribeFileFunc = func(context.Context, config.Config, string, io.Writer) (asr.Transcript, int) {
		t.Fatal("transcribe should not run with invalid ASR config")
		return asr.Transcript{}, exitcode.Success
	}
	path := writeConfig(t, `
[asr]
engine = "other"
`)
	var out, err bytes.Buffer
	if got := run([]string{"transcribe", "--config", path, "--file", "sample.wav"}, &out, &err); got != exitcode.ModelInvalid {
		t.Fatalf("exit = %d, want %d; stdout=%s stderr=%s", got, exitcode.ModelInvalid, out.String(), err.String())
	}
}

func TestBenchRejectsInvalidASRConfig(t *testing.T) {
	restore := replaceTranscribeDeps(t)
	defer restore()
	validateModelForUseFn = func(config.Config) error {
		t.Fatal("model validation should not run with invalid ASR config")
		return nil
	}
	readAudioFileFunc = func(string) (audio.FileAudio, error) {
		t.Fatal("audio file should not be read with invalid ASR config")
		return audio.FileAudio{}, nil
	}
	path := writeConfig(t, `
[asr]
provider = "cuda"
`)
	var out, err bytes.Buffer
	if got := run([]string{"bench", "--config", path, "--file", "sample.wav"}, &out, &err); got != exitcode.ModelInvalid {
		t.Fatalf("exit = %d, want %d; stdout=%s stderr=%s", got, exitcode.ModelInvalid, out.String(), err.String())
	}
}

func TestBenchUsesInputDurationWhenTranscriptOmitsIt(t *testing.T) {
	restore := replaceTranscribeDeps(t)
	defer restore()
	validateModelForUseFn = func(config.Config) error { return nil }
	readAudioFileFunc = func(string) (audio.FileAudio, error) {
		return audio.FileAudio{
			Samples:    []float32{0, 0},
			SampleRate: 2,
			Duration:   time.Second,
		}, nil
	}
	newASREngine = func(config.ASR) asr.Engine {
		return fakeEngine{transcript: asr.Transcript{SegmentID: "bench"}}
	}
	var out, err bytes.Buffer
	if got := run([]string{"bench", "--file", "sample.wav"}, &out, &err); got != exitcode.Success {
		t.Fatalf("exit = %d, want %d; stdout=%s stderr=%s", got, exitcode.Success, out.String(), err.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["audio_seconds"] != float64(1) {
		t.Fatalf("audio_seconds = %v, want 1", payload["audio_seconds"])
	}
	rtf, ok := payload["rtf"].(float64)
	if !ok || math.IsInf(rtf, 0) || math.IsNaN(rtf) {
		t.Fatalf("invalid rtf: %v", payload["rtf"])
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

func TestPrintDoctorModelWarningsAreNonFatal(t *testing.T) {
	var out bytes.Buffer
	ok := printDoctorModel(&out, model.CheckResult{
		OK:       true,
		Warnings: []string{"LICENSE missing"},
	})
	if !ok {
		t.Fatal("warning-only model check was treated as fatal")
	}
	if got := out.String(); got != "OK   model             \nWARN model              LICENSE missing\n" {
		t.Fatalf("output = %q", got)
	}
}

func TestPrintDoctorModelFailure(t *testing.T) {
	var out bytes.Buffer
	ok := printDoctorModel(&out, model.CheckResult{
		OK:     false,
		Errors: []string{"tokens.txt is empty"},
	})
	if ok {
		t.Fatal("failed model check was treated as ok")
	}
	if got := out.String(); got != "FAIL model              tokens.txt is empty\n" {
		t.Fatalf("output = %q", got)
	}
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func replaceTranscribeDeps(t *testing.T) func() {
	t.Helper()
	oldTranscribe := transcribeFileFunc
	oldInjector := newInjector
	oldGuard := newFocusGuard
	oldReadAudioFile := readAudioFileFunc
	oldNewASREngine := newASREngine
	oldValidateModel := validateModelForUseFn
	return func() {
		transcribeFileFunc = oldTranscribe
		newInjector = oldInjector
		newFocusGuard = oldGuard
		readAudioFileFunc = oldReadAudioFile
		newASREngine = oldNewASREngine
		validateModelForUseFn = oldValidateModel
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

type fakeEngine struct {
	transcript asr.Transcript
	err        error
	loaded     bool
}

func (f fakeEngine) Name() string { return "fake" }

func (f fakeEngine) Load(context.Context) error {
	return f.err
}

func (f fakeEngine) Close() error { return nil }

func (f fakeEngine) Loaded() bool { return f.loaded }

func (f fakeEngine) Transcribe(context.Context, asr.AudioSegment) (asr.Transcript, error) {
	return f.transcript, f.err
}
