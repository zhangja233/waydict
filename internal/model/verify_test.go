package model

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"waydict/internal/asr"
	"waydict/internal/config"
)

func TestCheckDirAcceptsReadableRequiredFiles(t *testing.T) {
	dir := writeTinyModel(t)
	res := CheckDir(dir, CheckOptions{})
	if !res.OK {
		t.Fatalf("check failed: %+v", res.Errors)
	}
}

func TestCheckDirWarnsOnMissingMetadata(t *testing.T) {
	dir := writeTinyModel(t)
	res := CheckDir(dir, CheckOptions{})
	if !res.OK {
		t.Fatalf("check failed: %+v", res.Errors)
	}
	if len(res.Warnings) != len(MetadataFiles()) {
		t.Fatalf("warnings = %v, want one per metadata file", res.Warnings)
	}
}

func TestCheckDirAcceptsMetadataFiles(t *testing.T) {
	dir := writeTinyModel(t)
	for _, name := range MetadataFiles() {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	res := CheckDir(dir, CheckOptions{})
	if !res.OK {
		t.Fatalf("check failed: %+v", res.Errors)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", res.Warnings)
	}
}

func TestCheckDirRejectsUnreadableRequiredFile(t *testing.T) {
	dir := writeTinyModel(t)
	path := filepath.Join(dir, "encoder.onnx")
	if err := os.Chmod(path, 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(path, 0644)
	})
	res := CheckDir(dir, CheckOptions{})
	if res.OK {
		t.Fatalf("check unexpectedly passed: %+v", res)
	}
}

func TestCheckDirRejectsUnsafeChecksumPath(t *testing.T) {
	dir := writeTinyModel(t)
	if err := os.WriteFile(filepath.Join(dir, DefaultChecksumFile), []byte("abcd  ../outside\n"), 0644); err != nil {
		t.Fatal(err)
	}
	res := CheckDir(dir, CheckOptions{})
	if res.OK {
		t.Fatalf("check unexpectedly passed: %+v", res)
	}
}

func TestCheckDirRejectsAbsoluteChecksumPath(t *testing.T) {
	dir := writeTinyModel(t)
	if err := os.WriteFile(filepath.Join(dir, DefaultChecksumFile), []byte("abcd  /tmp/outside\n"), 0644); err != nil {
		t.Fatal(err)
	}
	res := CheckDir(dir, CheckOptions{})
	if res.OK {
		t.Fatalf("check unexpectedly passed: %+v", res)
	}
}

func TestCheckConfigUsesConfiguredModelFiles(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"custom-encoder.onnx": "encoder",
		"custom-decoder.onnx": "decoder",
		"custom-joiner.onnx":  "joiner",
		"custom-tokens.txt":   "a\nb\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Defaults()
	cfg.ASR.Engine = asr.EngineSherpa
	cfg.ASR.Provider = asr.ProviderCPU
	cfg.ASR.ModelDir = dir
	cfg.ASR.Encoder = "custom-encoder.onnx"
	cfg.ASR.Decoder = "custom-decoder.onnx"
	cfg.ASR.Joiner = "custom-joiner.onnx"
	cfg.ASR.Tokens = "custom-tokens.txt"
	res := CheckConfig(cfg, CheckOptions{})
	if !res.OK {
		t.Fatalf("check failed: %+v", res.Errors)
	}
	got := map[string]bool{}
	for _, item := range res.Items {
		got[filepath.Base(item.Path)] = item.OK
	}
	for name := range files {
		if !got[name] {
			t.Fatalf("configured file %s was not checked successfully: %+v", name, res.Items)
		}
	}
}

func TestCheckConfigRejectsMissingConfiguredModelFile(t *testing.T) {
	dir := writeTinyModel(t)
	cfg := config.Defaults()
	cfg.ASR.Engine = asr.EngineSherpa
	cfg.ASR.Provider = asr.ProviderCPU
	cfg.ASR.ModelDir = dir
	cfg.ASR.Tokens = "missing-tokens.txt"
	res := CheckConfig(cfg, CheckOptions{})
	if res.OK {
		t.Fatalf("check unexpectedly passed: %+v", res)
	}
	found := false
	for _, item := range res.Items {
		if filepath.Base(item.Path) == "missing-tokens.txt" && !item.OK {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing configured tokens file was not reported: %+v", res.Items)
	}
}

func TestCheckConfigRequiresUnifiedEncoderWeights(t *testing.T) {
	dir := writeTinyModel(t)
	if err := os.Remove(filepath.Join(dir, "encoder.weights")); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.ASR.Engine = asr.EngineSherpa
	cfg.ASR.Provider = asr.ProviderCPU
	cfg.ASR.ModelDir = dir
	res := CheckConfig(cfg, CheckOptions{})
	if res.OK {
		t.Fatalf("check unexpectedly passed: %+v", res)
	}
	found := false
	for _, item := range res.Items {
		if filepath.Base(item.Path) == "encoder.weights" && !item.OK {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing encoder weights were not reported: %+v", res.Items)
	}
}

func TestCheckConfigEngineMatrix(t *testing.T) {
	tests := []struct {
		name           string
		engine         string
		sherpaPresent  bool
		whisperPresent bool
		wantOK         bool
		wantEngines    []string
	}{
		{name: "sherpa present", engine: asr.EngineSherpa, sherpaPresent: true, wantOK: true, wantEngines: []string{asr.EngineSherpa}},
		{name: "sherpa does not use whisper", engine: asr.EngineSherpa, whisperPresent: true},
		{name: "whisper present", engine: asr.EngineWhisper, whisperPresent: true, wantOK: true, wantEngines: []string{asr.EngineWhisper}},
		{name: "whisper does not use sherpa", engine: asr.EngineWhisper, sherpaPresent: true},
		{name: "auto sherpa", engine: asr.EngineAuto, sherpaPresent: true, wantOK: true, wantEngines: []string{asr.EngineSherpa}},
		{name: "auto whisper", engine: asr.EngineAuto, whisperPresent: true, wantOK: true, wantEngines: []string{asr.EngineWhisper}},
		{name: "auto both", engine: asr.EngineAuto, sherpaPresent: true, whisperPresent: true, wantOK: true, wantEngines: []string{asr.EngineSherpa, asr.EngineWhisper}},
		{name: "auto neither", engine: asr.EngineAuto},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			cfg := config.Defaults()
			cfg.ASR.Engine = tt.engine
			if tt.engine == asr.EngineSherpa {
				cfg.ASR.Provider = asr.ProviderCPU
			}
			cfg.ASR.ModelDir = filepath.Join(t.TempDir(), "missing-parakeet")
			if tt.sherpaPresent {
				cfg.ASR.ModelDir = writePlausibleModel(t)
			}
			if tt.whisperPresent {
				writePlausibleWhisperModel(t, cfg)
			}
			res := CheckConfig(cfg, CheckOptions{StrictSizes: true})
			if res.OK != tt.wantOK {
				t.Fatalf("ok = %t, want %t; errors=%v", res.OK, tt.wantOK, res.Errors)
			}
			if len(res.Validated) != len(tt.wantEngines) {
				t.Fatalf("validated = %+v, want engines %v", res.Validated, tt.wantEngines)
			}
			for i, want := range tt.wantEngines {
				if res.Validated[i].Engine != want {
					t.Fatalf("validated[%d].engine = %q, want %q", i, res.Validated[i].Engine, want)
				}
			}
			if tt.engine == asr.EngineAuto && !tt.wantOK {
				if len(res.Errors) != 1 || !strings.Contains(res.Errors[0], "sherpa-onnx:") || !strings.Contains(res.Errors[0], "whisper-cpp:") {
					t.Fatalf("combined auto error = %v", res.Errors)
				}
			}
		})
	}
}

func TestCheckConfigRejectsTinyWhisperModel(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := config.Defaults()
	cfg.ASR.Engine = asr.EngineWhisper
	path := cfg.WhisperModelPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("tiny"), 0644); err != nil {
		t.Fatal(err)
	}
	res := CheckConfig(cfg, CheckOptions{StrictSizes: true})
	if res.OK || len(res.Errors) == 0 || !strings.Contains(strings.Join(res.Errors, " "), "implausibly small") {
		t.Fatalf("tiny whisper model check = %+v", res)
	}
}

func TestCheckDirRejectsWhisperFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ggml-small.en.bin")
	if err := os.WriteFile(path, []byte("model"), 0644); err != nil {
		t.Fatal(err)
	}
	res := CheckDir(path, CheckOptions{})
	if res.OK || len(res.Errors) != 1 || !strings.Contains(res.Errors[0], "not a parakeet model directory") {
		t.Fatalf("whisper file check = %+v", res)
	}
}

func TestCheckDirAcceptsLegacyInt8Layout(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"encoder.int8.onnx": "encoder",
		"decoder.int8.onnx": "decoder",
		"joiner.int8.onnx":  "joiner",
		"tokens.txt":        "a\nb\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	res := CheckDir(dir, CheckOptions{})
	if !res.OK {
		t.Fatalf("check failed: %+v", res.Errors)
	}
}

func TestCheckVADConfigSileroMissingWarnsButOK(t *testing.T) {
	cfg := config.Defaults()
	cfg.VAD.Engine = "silero"
	cfg.VAD.Model = filepath.Join(t.TempDir(), "absent.onnx")
	res := CheckVADConfig(cfg)
	if !res.OK {
		t.Fatalf("missing silero model must not be fatal: %+v", res)
	}
	if res.Warning == "" {
		t.Fatal("expected a warning for the missing silero model")
	}
}

func TestCheckVADConfigSileroPresent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "silero_vad.onnx")
	if err := os.WriteFile(path, []byte("model"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.VAD.Engine = "silero"
	cfg.VAD.Model = path
	res := CheckVADConfig(cfg)
	if !res.OK || res.Warning != "" {
		t.Fatalf("present silero model: ok=%t warning=%q", res.OK, res.Warning)
	}
}

func TestCheckVADConfigEnergyNeedsNoModel(t *testing.T) {
	cfg := config.Defaults()
	cfg.VAD.Engine = "energy"
	cfg.VAD.Model = filepath.Join(t.TempDir(), "absent.onnx")
	res := CheckVADConfig(cfg)
	if !res.OK || res.Warning != "" {
		t.Fatalf("energy engine: ok=%t warning=%q", res.OK, res.Warning)
	}
}

func writeTinyModel(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"encoder.onnx":    "encoder",
		"encoder.weights": "weights",
		"decoder.onnx":    "decoder",
		"joiner.onnx":     "joiner",
		"tokens.txt":      "a\nb\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func writePlausibleModel(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, req := range ParakeetUnifiedFP32Files() {
		path := filepath.Join(dir, req.Name)
		if req.Name == "tokens.txt" {
			if err := os.WriteFile(path, []byte("a\nb\nc\nd\ne\nf\ng\nh\ni\nj\nk\nl\nm\nn\no\np\n"), 0644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Truncate(req.MinSize); err != nil {
			_ = f.Close()
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func writePlausibleWhisperModel(t *testing.T, cfg config.Config) {
	t.Helper()
	path := cfg.WhisperModelPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(WhisperModelMinSize(cfg.ASR.WhisperModel)); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}
