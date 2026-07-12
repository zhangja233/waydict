package config

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"waydict/internal/asr"
)

func TestLoadExpandValidate(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", filepath.Join(tmp, "home"))
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(tmp, "run"))
	t.Setenv("SWAYSOCK", filepath.Join(tmp, "sway.sock"))
	t.Setenv("USER", "tester")
	path := filepath.Join(tmp, "config.toml")
	if err := os.WriteFile(path, []byte(`
[daemon]
socket = "$XDG_RUNTIME_DIR/waydict/waydict.sock"
[asr]
num_threads = 1
model_dir = "~/models/parakeet"
whisper_model = "$USER-small.en"
`), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Daemon.Socket != filepath.Join(tmp, "run", "waydict", "waydict.sock") {
		t.Fatalf("socket was not expanded: %q", cfg.Daemon.Socket)
	}
	if cfg.ASR.ModelDir != filepath.Join(tmp, "home", "models", "parakeet") {
		t.Fatalf("home was not expanded: %q", cfg.ASR.ModelDir)
	}
	if cfg.ASR.WhisperModel != "tester-small.en" {
		t.Fatalf("whisper model was not expanded: %q", cfg.ASR.WhisperModel)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultPathPrefersFlatFileThenDirForm(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	flat := filepath.Join(home, ".config", "waydict.toml")
	dir := filepath.Join(home, ".config", "waydict", "config.toml")

	// Neither present: preferred (flat) path is returned.
	if got := DefaultPath(); got != flat {
		t.Fatalf("no config: DefaultPath() = %q, want %q", got, flat)
	}

	// Only the directory form present: it is used (backward compatible).
	if err := os.MkdirAll(filepath.Dir(dir), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	if got := DefaultPath(); got != dir {
		t.Fatalf("dir only: DefaultPath() = %q, want %q", got, dir)
	}

	// Both present: the flat file wins.
	if err := os.WriteFile(flat, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	if got := DefaultPath(); got != flat {
		t.Fatalf("both present: DefaultPath() = %q, want %q", got, flat)
	}
}

func TestDefaultPathHonorsXDGConfigHome(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	want := []string{
		filepath.Join(xdg, "waydict.toml"),
		filepath.Join(xdg, "waydict", "config.toml"),
	}
	got := DefaultPaths()
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("DefaultPaths() = %v, want %v", got, want)
	}
}

func TestLoadReadsFlatConfigFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(home, "run"))
	t.Setenv("USER", "tester")
	flat := filepath.Join(home, ".config", "waydict.toml")
	if err := os.MkdirAll(filepath.Dir(flat), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(flat, []byte("[asr]\nnum_threads = 3\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ASR.NumThreads != 3 {
		t.Fatalf("flat config not loaded: num_threads = %d, want 3", cfg.ASR.NumThreads)
	}
}

func TestLoadAppliesEngineConditionalProviderDefaults(t *testing.T) {
	tests := []struct {
		engine string
		want   string
	}{
		{engine: asr.EngineAuto, want: ""},
		{engine: asr.EngineSherpa, want: asr.ProviderCPU},
		{engine: asr.EngineWhisper, want: asr.ProviderVulkan},
	}
	for _, tc := range tests {
		t.Run(tc.engine, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte("[asr]\nengine = \""+tc.engine+"\"\n"), 0644); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.ASR.Provider != tc.want {
				t.Fatalf("provider = %q, want %q", cfg.ASR.Provider, tc.want)
			}
		})
	}
}

func TestWhisperModelPathUsesSharedModelsRoot(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := Defaults()
	cfg.ASR.ModelDir = ""
	got := cfg.WhisperModelPath()
	want := filepath.Join(DefaultModelsRoot(), "whisper", "ggml-large-v3-turbo.bin")
	if got != want {
		t.Fatalf("WhisperModelPath() = %q, want %q", got, want)
	}
}

func TestExpandPathRejectsUnknownVariable(t *testing.T) {
	if _, err := ExpandPath("$NOT_ALLOWED/path"); err == nil {
		t.Fatal("expected unknown variable error")
	}
}

func TestValidateASRDoesNotRequireDaemonRuntimeSettings(t *testing.T) {
	cfg := Defaults()
	cfg.ASR.NumThreads = 1
	cfg.Audio.Backend = "alsa"
	cfg.Sway.RequireSway = false
	if err := cfg.ValidateASR(); err != nil {
		t.Fatal(err)
	}
	cfg.ASR.Engine = "other"
	if err := cfg.ValidateASR(); err == nil {
		t.Fatal("expected ASR validation error")
	}
}

func TestValidateASREngineMatrix(t *testing.T) {
	tests := []struct {
		name    string
		edit    func(*Config)
		wantErr bool
	}{
		{name: "auto empty provider", edit: func(c *Config) { c.ASR.Provider = "" }},
		{name: "auto cpu", edit: func(c *Config) { c.ASR.Provider = asr.ProviderCPU }},
		{name: "auto vulkan", edit: func(c *Config) { c.ASR.Provider = asr.ProviderVulkan }},
		{name: "auto ignores sherpa shape", edit: func(c *Config) {
			c.ASR.ModelDir = ""
			c.ASR.Encoder = ""
			c.ASR.ModelType = ""
			c.ASR.NumThreads = 0
		}},
		{name: "auto rejects provider", edit: func(c *Config) { c.ASR.Provider = "cuda" }, wantErr: true},
		{name: "whisper cpu", edit: func(c *Config) {
			c.ASR.Engine = asr.EngineWhisper
			c.ASR.Provider = asr.ProviderCPU
			c.ASR.ModelDir = ""
			c.ASR.Encoder = ""
		}},
		{name: "whisper vulkan", edit: func(c *Config) {
			c.ASR.Engine = asr.EngineWhisper
			c.ASR.Provider = asr.ProviderVulkan
		}},
		{name: "whisper empty defaults logically", edit: func(c *Config) {
			c.ASR.Engine = asr.EngineWhisper
			c.ASR.Provider = ""
		}},
		{name: "whisper rejects provider", edit: func(c *Config) {
			c.ASR.Engine = asr.EngineWhisper
			c.ASR.Provider = "cuda"
		}, wantErr: true},
		{name: "whisper requires model", edit: func(c *Config) {
			c.ASR.Engine = asr.EngineWhisper
			c.ASR.WhisperModel = ""
		}, wantErr: true},
		{name: "sherpa cpu", edit: func(c *Config) {
			c.ASR.Engine = asr.EngineSherpa
			c.ASR.Provider = asr.ProviderCPU
		}},
		{name: "sherpa empty defaults logically", edit: func(c *Config) {
			c.ASR.Engine = asr.EngineSherpa
			c.ASR.Provider = ""
		}},
		{name: "sherpa rejects vulkan", edit: func(c *Config) {
			c.ASR.Engine = asr.EngineSherpa
			c.ASR.Provider = asr.ProviderVulkan
		}, wantErr: true},
		{name: "sherpa validates shape", edit: func(c *Config) {
			c.ASR.Engine = asr.EngineSherpa
			c.ASR.Provider = asr.ProviderCPU
			c.ASR.ModelDir = ""
		}, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Defaults()
			cfg.ASR.NumThreads = 1
			tc.edit(&cfg)
			err := cfg.ValidateASR()
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateASR() error = %v, wantErr %t", err, tc.wantErr)
			}
		})
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
			cfg.ASR.Engine = asr.EngineSherpa
			cfg.ASR.Provider = asr.ProviderCPU
			cfg.ASR.NumThreads = 1
			tc.edit(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
