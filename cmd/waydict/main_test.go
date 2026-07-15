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
	"strings"
	"testing"
	"time"

	"waydict/internal/apperr"
	"waydict/internal/asr"
	"waydict/internal/audio"
	"waydict/internal/config"
	"waydict/internal/control"
	"waydict/internal/exitcode"
	"waydict/internal/focus"
	"waydict/internal/inject"
	"waydict/internal/model"
	"waydict/internal/modelinstall"
	"waydict/pkg/api"
)

func TestRunUsage(t *testing.T) {
	var out, err bytes.Buffer
	if got := run(nil, &out, &err); got != exitcode.Usage {
		t.Fatalf("exit = %d, want %d", got, exitcode.Usage)
	}
}

func TestExitForErrUsesTypedCodes(t *testing.T) {
	if got := exitForErr(errors.New("audio backend unavailable")); got != exitcode.Generic {
		t.Fatalf("unknown error exit = %d", got)
	}
	err := apperr.New(apperr.CodeAudioBackendUnavailable, "start audio", errors.New("unavailable"))
	if got := exitForErr(err); got != exitcode.PipeWireUnavailable {
		t.Fatalf("typed error exit = %d", got)
	}
}

func TestStatusDaemonUnavailable(t *testing.T) {
	forceCLIPlatform(t, "linux")
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
	forceCLIPlatform(t, "linux")
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
	forceCLIPlatform(t, "linux")
	restore := replaceTranscribeDeps(t)
	defer restore()
	guard := &fakeFocusGuard{checkErr: apperr.New(apperr.CodeFocusChanged, "compare focus", errors.New("focus changed from 1 to 2"))}
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

func TestTranscribeInjectWarnAndTypeReportsWarning(t *testing.T) {
	forceCLIPlatform(t, "linux")
	restore := replaceTranscribeDeps(t)
	defer restore()
	guard := &fakeFocusGuard{
		warning: &focus.Change{
			From: focus.Target{StableID: "1"},
			To:   focus.Target{StableID: "2"},
		},
	}
	typed := ""
	newFocusGuard = func(config.Config) focusGuard { return guard }
	newInjector = func(config.Injection) inject.Injector {
		return fakeInjector{typeText: func(_ context.Context, text string) error {
			typed = text
			return nil
		}}
	}
	transcribeFileFunc = func(context.Context, config.Config, string, io.Writer) (asr.Transcript, int) {
		return asr.Transcript{Text: "hello"}, exitcode.Success
	}
	var out, err bytes.Buffer
	if got := run([]string{"transcribe", "--file", "sample.wav", "--inject"}, &out, &err); got != exitcode.Success {
		t.Fatalf("exit = %d, want %d; stdout=%s stderr=%s", got, exitcode.Success, out.String(), err.String())
	}
	if typed != "hello " {
		t.Fatalf("typed = %q", typed)
	}
	if got := err.String(); !strings.Contains(got, "warning: focus changed from 1 to 2") {
		t.Fatalf("stderr = %q", got)
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

func TestResolveForcedWhisperWithoutHookFails(t *testing.T) {
	restore := replaceTranscribeDeps(t)
	defer restore()
	newWhisperEngineHook = nil
	cfg := config.Defaults()
	cfg.ASR.Engine = asr.EngineWhisper
	cfg.ASR.Provider = asr.ProviderVulkan
	if _, _, err := resolveASREngine(cfg); err == nil || !strings.Contains(err.Error(), "not built in") {
		t.Fatalf("resolve error = %v, want missing whisper build", err)
	}
}

func TestResolveAutoFallsBackWithReason(t *testing.T) {
	restore := replaceTranscribeDeps(t)
	defer restore()
	newWhisperEngineHook = nil
	cfg := config.Defaults()
	_, resolution, err := resolveASREngine(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Engine != asr.EngineSherpa || resolution.Provider != asr.ProviderCPU || resolution.FallbackReason == "" {
		t.Fatalf("resolution = %+v, want sherpa fallback with reason", resolution)
	}
}

func TestResolveAutoSurfacesSherpaShapeErrorsAfterFallback(t *testing.T) {
	restore := replaceTranscribeDeps(t)
	defer restore()
	newWhisperEngineHook = nil
	cfg := config.Defaults()
	cfg.ASR.ModelDir = ""
	if _, _, err := resolveASREngine(cfg); err == nil || !strings.Contains(err.Error(), "asr.model_dir") {
		t.Fatalf("resolve error = %v, want resolved sherpa config error", err)
	}
}

func TestAutoLoadFailureFallsBackToSherpa(t *testing.T) {
	restore := replaceTranscribeDeps(t)
	defer restore()
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := config.Defaults()
	cfg.ASR.NumThreads = 1
	modelPath := cfg.WhisperModelPath()
	if err := os.MkdirAll(filepath.Dir(modelPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(modelPath, []byte("model"), 0644); err != nil {
		t.Fatal(err)
	}
	newWhisperEngineHook = func(string, int, int, bool) (asr.Engine, error) {
		return fakeEngine{err: errors.New("vulkan allocation failed")}, nil
	}
	probeGPUHook = func() (string, error) { return "test gpu", nil }
	newASREngine = func(config.ASR) asr.Engine {
		return fakeEngine{transcript: asr.Transcript{Text: "fallback"}}
	}
	validateModelForUseFn = func(config.Config) error { return nil }
	engine, resolution, err := resolveASREngine(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	engine, resolution, err = loadResolvedASR(context.Background(), cfg, engine, resolution, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	if resolution.Engine != asr.EngineSherpa || resolution.FallbackReason == "" {
		t.Fatalf("resolution = %+v, want load-time sherpa fallback", resolution)
	}
	if got := stderr.String(); !strings.Contains(got, "whisper-cpp load failed") || !strings.Contains(got, "falling back to sherpa-onnx") {
		t.Fatalf("stderr = %q", got)
	}
}

func TestPrintStatusIncludesResolvedASR(t *testing.T) {
	var out bytes.Buffer
	printStatus(&out, api.Status{
		State: api.StateIdle,
		ASR: api.ASRStatus{
			Engine:           asr.EngineAuto,
			ResolvedEngine:   asr.EngineWhisper,
			ResolvedProvider: asr.ProviderVulkan,
			GPUName:          "test gpu",
		},
	})
	if got := out.String(); !strings.Contains(got, "asr=whisper-cpp provider=vulkan gpu=test gpu") {
		t.Fatalf("status output = %q", got)
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

func TestPrintDoctorVADWarnsWhenSileroModelMissing(t *testing.T) {
	var out bytes.Buffer
	printDoctorVAD(&out, model.VADCheckResult{Engine: "silero", Model: "/x/silero_vad.onnx", OK: true, Warning: "silero model missing"})
	if got := out.String(); !strings.HasPrefix(got, "WARN vad model") {
		t.Fatalf("output = %q", got)
	}
}

func TestPrintDoctorVADOKForEnergyEngine(t *testing.T) {
	var out bytes.Buffer
	printDoctorVAD(&out, model.VADCheckResult{Engine: "energy", OK: true})
	got := out.String()
	if !strings.HasPrefix(got, "OK   vad model") || !strings.Contains(got, "no model needed") {
		t.Fatalf("output = %q", got)
	}
}

func TestModelInstallRejectsUnknownName(t *testing.T) {
	var out, err bytes.Buffer
	if got := run([]string{"model", "install", "bogus/name"}, &out, &err); got != exitcode.Usage {
		t.Fatalf("exit = %d, want %d; stderr=%s", got, exitcode.Usage, err.String())
	}
}

func TestModelInstallRequiresName(t *testing.T) {
	var out, err bytes.Buffer
	if got := run([]string{"model", "install"}, &out, &err); got != exitcode.Usage {
		t.Fatalf("exit = %d, want %d; stderr=%s", got, exitcode.Usage, err.String())
	}
}

func TestModelInstallUsageDescribesWhisperNames(t *testing.T) {
	var out bytes.Buffer
	usage(&out)
	for _, text := range []string{"whisper-model-name", model.WhisperLargeV3TurboModel, "integrity-pinned", "size-checked"} {
		if !strings.Contains(out.String(), text) {
			t.Fatalf("usage does not contain %q: %s", text, out.String())
		}
	}
}

func TestModelInstallRoutesWhisperName(t *testing.T) {
	oldWhisper := installWhisper
	t.Cleanup(func() { installWhisper = oldWhisper })
	var gotName string
	installWhisper = func(_ context.Context, name string, _ modelinstall.InstallOptions) (string, error) {
		gotName = name
		return "/models/whisper/ggml-base.en.bin", nil
	}
	var out, err bytes.Buffer
	if got := run([]string{"model", "install", "ggml-base.en", "--dir", t.TempDir()}, &out, &err); got != exitcode.Success {
		t.Fatalf("exit = %d; stdout=%s stderr=%s", got, out.String(), err.String())
	}
	if gotName != "ggml-base.en" {
		t.Fatalf("whisper install name = %q", gotName)
	}
}

func TestModelInstallAllIncludesDefaultWhisper(t *testing.T) {
	oldUnified := installParakeetUnifiedFP32
	oldV3 := installParakeetV3Int8
	oldSilero := installSileroVAD
	oldWhisper := installWhisper
	t.Cleanup(func() {
		installParakeetUnifiedFP32 = oldUnified
		installParakeetV3Int8 = oldV3
		installSileroVAD = oldSilero
		installWhisper = oldWhisper
	})
	var calls []string
	installParakeetUnifiedFP32 = func(context.Context, modelinstall.InstallOptions) (string, error) {
		calls = append(calls, model.ParakeetUnifiedFP32ID)
		return "/models/parakeet", nil
	}
	installSileroVAD = func(context.Context, modelinstall.InstallOptions) (string, error) {
		calls = append(calls, "silero-vad")
		return "/models/silero", nil
	}
	installWhisper = func(_ context.Context, name string, _ modelinstall.InstallOptions) (string, error) {
		calls = append(calls, name)
		return "/models/whisper", nil
	}
	var out, err bytes.Buffer
	if got := run([]string{"model", "install", "all", "--dir", t.TempDir()}, &out, &err); got != exitcode.Success {
		t.Fatalf("exit = %d; stdout=%s stderr=%s", got, out.String(), err.String())
	}
	want := []string{model.ParakeetUnifiedFP32ID, "silero-vad", config.Defaults().ASR.WhisperModel}
	if strings.Join(calls, ",") != strings.Join(want, ",") {
		t.Fatalf("install calls = %v, want %v", calls, want)
	}
}

func TestModelCheckJSONReportsWhisperModel(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := config.Defaults()
	path := cfg.WhisperModelPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(model.WhisperModelMinSize(cfg.ASR.WhisperModel)); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	configPath := writeConfig(t, `
[asr]
engine = "whisper-cpp"
provider = "cpu"
`)
	var out, stderr bytes.Buffer
	if got := run([]string{"model", "check", "--config", configPath, "--json"}, &out, &stderr); got != exitcode.Success {
		t.Fatalf("exit = %d; stdout=%s stderr=%s", got, out.String(), stderr.String())
	}
	var res model.CheckResult
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Engine != asr.EngineWhisper || len(res.Validated) != 1 || res.Validated[0].Engine != asr.EngineWhisper || res.Validated[0].Path != path {
		t.Fatalf("model check result = %+v", res)
	}
}

func TestCommandClassRouting(t *testing.T) {
	tests := []struct {
		platform string
		args     []string
		want     commandClass
	}{
		{platform: "darwin", args: []string{"status"}, want: commandDaemonDependent},
		{platform: "linux", args: []string{"status"}, want: commandClient},
		{platform: "darwin", args: []string{"transcribe", "--file", "x.wav"}, want: commandLocalOnly},
		{platform: "darwin", args: []string{"transcribe", "--file", "x.wav", "--inject"}, want: commandDaemonDependent},
		{platform: "linux", args: []string{"daemon"}, want: commandLocalOnly},
		{platform: "darwin", args: []string{"daemon"}, want: commandDaemonDependent},
	}
	for _, tc := range tests {
		if got := commandClassFor(tc.platform, tc.args); got != tc.want {
			t.Errorf("%s %v = %s, want %s", tc.platform, tc.args, got, tc.want)
		}
	}
}

func TestDarwinNoLaunchSkipsLauncher(t *testing.T) {
	forceCLIPlatform(t, "darwin")
	oldSend, oldLaunch := controlSend, launchAppBundle
	t.Cleanup(func() { controlSend, launchAppBundle = oldSend, oldLaunch })
	controlSend = func(context.Context, string, control.Request) (control.Response, error) {
		return control.Response{}, os.ErrNotExist
	}
	launched := false
	launchAppBundle = func(context.Context, string) error {
		launched = true
		return nil
	}
	_, err := sendRuntimeRequest(context.Background(), "/tmp/waydict-501/control.sock", control.NewRequest("status", nil), cliOptions{noLaunch: true})
	if !errors.Is(err, os.ErrNotExist) || launched {
		t.Fatalf("error=%v launched=%t", err, launched)
	}
}

func TestDarwinTranscribeInjectDelegatesToApp(t *testing.T) {
	restore := replaceTranscribeDeps(t)
	defer restore()
	forceCLIPlatform(t, "darwin")
	oldSend := controlSend
	t.Cleanup(func() { controlSend = oldSend })
	newInjector = func(config.Injection) inject.Injector {
		t.Fatal("Darwin CLI constructed an injector")
		return nil
	}
	newFocusGuard = func(config.Config) focusGuard {
		t.Fatal("Darwin CLI constructed a focus guard")
		return nil
	}
	transcribeFileFunc = func(context.Context, config.Config, string, io.Writer) (asr.Transcript, int) {
		return asr.Transcript{Text: "hello"}, exitcode.Success
	}
	var request control.Request
	controlSend = func(_ context.Context, _ string, req control.Request) (control.Response, error) {
		request = req
		return control.OK(req.ID, api.Status{}), nil
	}
	var stdout, stderr bytes.Buffer
	if got := run([]string{"transcribe", "--file", "sample.wav", "--inject"}, &stdout, &stderr); got != exitcode.Success {
		t.Fatalf("exit=%d stdout=%s stderr=%s", got, stdout.String(), stderr.String())
	}
	if request.Command != "inject_text" || request.Args["text"] != "hello " {
		t.Fatalf("request = %+v", request)
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
	// Isolate from the real user model dirs and tagged hooks: auto-engine
	// resolution must never see ~/.local/share/waydict or a live GPU here.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", "")
	oldTranscribe := transcribeFileFunc
	oldInjector := newInjector
	oldGuard := newFocusGuard
	oldReadAudioFile := readAudioFileFunc
	oldNewASREngine := newASREngine
	oldValidateModel := validateModelForUseFn
	oldNewWhisper := newWhisperEngineHook
	oldProbeGPU := probeGPUHook
	newWhisperEngineHook = nil
	probeGPUHook = nil
	return func() {
		transcribeFileFunc = oldTranscribe
		newInjector = oldInjector
		newFocusGuard = oldGuard
		readAudioFileFunc = oldReadAudioFile
		newASREngine = oldNewASREngine
		validateModelForUseFn = oldValidateModel
		newWhisperEngineHook = oldNewWhisper
		probeGPUHook = oldProbeGPU
	}
}

func forceCLIPlatform(t *testing.T, platform string) {
	t.Helper()
	old := cliPlatform
	cliPlatform = platform
	t.Cleanup(func() { cliPlatform = old })
}

type fakeFocusGuard struct {
	captured bool
	checked  bool
	checkErr error
	warning  *focus.Change
}

func (f *fakeFocusGuard) CaptureStart(context.Context, int) error {
	f.captured = true
	return nil
}

func (f *fakeFocusGuard) ResolveForInjection(context.Context) (focus.Target, *focus.Change, error) {
	f.checked = true
	return focus.Target{}, f.warning, f.checkErr
}

func (f *fakeFocusGuard) Release(focus.Target)                         {}
func (f *fakeFocusGuard) Validate(context.Context, focus.Target) error { return nil }
func (f *fakeFocusGuard) Reset()                                       {}

type fakeInjector struct {
	typeText func(context.Context, string) error
}

func (f fakeInjector) Backend() string { return "fake" }

func (f fakeInjector) Available(context.Context) error {
	return nil
}

func (f fakeInjector) Inject(ctx context.Context, request inject.Request) error {
	if request.ValidateTarget != nil {
		if err := request.ValidateTarget(ctx, request.Target.Focus); err != nil {
			return err
		}
	}
	if f.typeText == nil {
		return nil
	}
	return f.typeText(ctx, request.Text)
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
