package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadExpandValidate(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", filepath.Join(tmp, "home"))
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(tmp, "run"))
	t.Setenv("SWAYSOCK", filepath.Join(tmp, "sway.sock"))
	path := filepath.Join(tmp, "config.toml")
	if err := os.WriteFile(path, []byte(`
[daemon]
socket = "$XDG_RUNTIME_DIR/sway-voice/sway-voice.sock"
[asr]
num_threads = 1
model_dir = "~/models/parakeet"
`), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Daemon.Socket != filepath.Join(tmp, "run", "sway-voice", "sway-voice.sock") {
		t.Fatalf("socket was not expanded: %q", cfg.Daemon.Socket)
	}
	if cfg.ASR.ModelDir != filepath.Join(tmp, "home", "models", "parakeet") {
		t.Fatalf("home was not expanded: %q", cfg.ASR.ModelDir)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestExpandPathRejectsUnknownVariable(t *testing.T) {
	if _, err := ExpandPath("$NOT_ALLOWED/path"); err == nil {
		t.Fatal("expected unknown variable error")
	}
}
