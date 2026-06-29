package inject

import (
	"testing"

	"sway-voice/internal/config"
)

func TestPostProcessorPunctuationSpacing(t *testing.T) {
	cfg := config.Defaults()
	p := NewPostProcessor(cfg.PostProcess, true)
	got := p.Apply(" Hello   ( world ) , test !")
	if got != "Hello (world), test! " {
		t.Fatalf("got %q", got)
	}
}

func TestSpokenCommands(t *testing.T) {
	cfg := config.Defaults()
	cfg.PostProcess.SpokenFormattingCommands = true
	p := NewPostProcessor(cfg.PostProcess, true)
	if got := p.Apply("new paragraph"); got != "\n\n" {
		t.Fatalf("got %q", got)
	}
	if got := p.Apply("scratch that"); got != "" {
		t.Fatalf("got %q", got)
	}
}
