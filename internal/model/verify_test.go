package model

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckDirAcceptsReadableRequiredFiles(t *testing.T) {
	dir := writeTinyModel(t)
	res := CheckDir(dir, CheckOptions{})
	if !res.OK {
		t.Fatalf("check failed: %+v", res.Errors)
	}
}

func TestCheckDirRejectsUnreadableRequiredFile(t *testing.T) {
	dir := writeTinyModel(t)
	path := filepath.Join(dir, "encoder.int8.onnx")
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

func writeTinyModel(t *testing.T) string {
	t.Helper()
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
	return dir
}
