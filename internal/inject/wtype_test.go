package inject

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"sway-voice/internal/config"
)

func TestWtypeUsesStdin(t *testing.T) {
	tmp := t.TempDir()
	out := filepath.Join(tmp, "typed.txt")
	fake := filepath.Join(tmp, "fake-wtype")
	script := "#!/bin/sh\ncat > \"$SVIP_WTYPE_OUT\"\n"
	if err := os.WriteFile(fake, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SVIP_WTYPE_OUT", out)
	cfg := config.Defaults().Injection
	cfg.WtypePath = fake
	w := NewWtype(cfg)
	if err := w.TypeText(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Fatalf("stdin = %q", data)
	}
}

func TestWtypeRejectsNonExecutablePath(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "not-executable")
	if err := os.WriteFile(path, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults().Injection
	cfg.WtypePath = path
	if err := NewWtype(cfg).Available(context.Background()); err == nil {
		t.Fatal("expected non-executable path error")
	}
}
