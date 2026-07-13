package app

import (
	"os"
	"path/filepath"
	"testing"

	"waydict/internal/asr"
	"waydict/internal/config"
)

func TestResolveDaemonASRPassesVocabularyPrompt(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", "")
	cfg := config.Defaults()
	cfg.ASR.Engine = asr.EngineWhisper
	cfg.ASR.Provider = asr.ProviderCPU
	cfg.ASR.Vocabulary = []string{"Claude", "Codex"}
	modelPath := cfg.WhisperModelPath()
	if err := os.MkdirAll(filepath.Dir(modelPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(modelPath, []byte("model"), 0644); err != nil {
		t.Fatal(err)
	}
	var initialPrompt string
	opts := DaemonOptions{
		NewWhisper: func(_ string, _ int, _ int, _ bool, stringPrompt string) (asr.Engine, error) {
			initialPrompt = stringPrompt
			return &FakeEngine{}, nil
		},
	}
	engine, _, err := resolveDaemonASR(cfg, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	if want := config.WhisperInitialPrompt(cfg.ASR.Vocabulary); initialPrompt != want {
		t.Fatalf("initial prompt = %q, want %q", initialPrompt, want)
	}
}
