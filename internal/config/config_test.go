package config

import (
	"math"
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

func TestValidateRejectsInvalidRuntimeBounds(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Config)
	}{
		{
			name: "daemon socket",
			edit: func(c *Config) { c.Daemon.Socket = "" },
		},
		{
			name: "daemon log level",
			edit: func(c *Config) { c.Daemon.LogLevel = "trace" },
		},
		{
			name: "format",
			edit: func(c *Config) { c.Audio.Format = "s16le" },
		},
		{
			name: "quantum",
			edit: func(c *Config) { c.Audio.QuantumMS = 0 },
		},
		{
			name: "auto stop",
			edit: func(c *Config) { c.Daemon.AutoStopAfterSilenceSeconds = -1 },
		},
		{
			name: "vad engine",
			edit: func(c *Config) { c.VAD.Engine = "unknown" },
		},
		{
			name: "silero model",
			edit: func(c *Config) { c.VAD.Model = "" },
		},
		{
			name: "vad threshold",
			edit: func(c *Config) { c.VAD.Threshold = 2 },
		},
		{
			name: "vad negative threshold",
			edit: func(c *Config) { c.VAD.NegativeThreshold = c.VAD.Threshold + 0.1 },
		},
		{
			name: "asr model type",
			edit: func(c *Config) { c.ASR.ModelType = "ctc" },
		},
		{
			name: "asr decoding method",
			edit: func(c *Config) { c.ASR.DecodingMethod = "modified_beam_search" },
		},
		{
			name: "asr model dir",
			edit: func(c *Config) { c.ASR.ModelDir = "" },
		},
		{
			name: "asr empty model file",
			edit: func(c *Config) { c.ASR.Encoder = "" },
		},
		{
			name: "asr absolute model file",
			edit: func(c *Config) { c.ASR.Decoder = filepath.Join(t.TempDir(), "decoder.int8.onnx") },
		},
		{
			name: "asr parent model file",
			edit: func(c *Config) { c.ASR.Joiner = "../joiner.int8.onnx" },
		},
		{
			name: "asr max active paths",
			edit: func(c *Config) { c.ASR.MaxActivePaths = 0 },
		},
		{
			name: "asr blank penalty",
			edit: func(c *Config) { c.ASR.BlankPenalty = float32(math.NaN()) },
		},
		{
			name: "injection delay",
			edit: func(c *Config) { c.Injection.KeyDelayMS = -1 },
		},
		{
			name: "injection timeout",
			edit: func(c *Config) { c.Injection.TimeoutMS = 0 },
		},
		{
			name: "injection wtype path",
			edit: func(c *Config) { c.Injection.WtypePath = "" },
		},
		{
			name: "debug save audio dir",
			edit: func(c *Config) {
				c.Debug.SaveAudioSegments = true
				c.Debug.SaveAudioDir = ""
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Defaults()
			cfg.ASR.NumThreads = 1
			tc.edit(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
