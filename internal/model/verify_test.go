package model

import (
	"os"
	"path/filepath"
	"testing"

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
