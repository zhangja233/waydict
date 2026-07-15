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

func TestLoadPostProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(`
[postprocess]
smart_case = false
`), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PostProcess.SmartCase {
		t.Fatal("smart_case was not decoded")
	}
}

func TestPostProcessDefaults(t *testing.T) {
	cfg := Defaults()
	if !cfg.PostProcess.SmartCase {
		t.Fatal("smart_case must default to true")
	}
}

func TestDefaultPathPrefersFlatFileThenDirForm(t *testing.T) {
	home := t.TempDir()
	flat := filepath.Join(home, ".config", "waydict.toml")
	dir := filepath.Join(home, ".config", "waydict", "config.toml")
	paths := PathsFor("linux", PathEnvironment{HomeDir: home, TempDir: "/tmp", User: "tester"})
	got := ConfigSearchPathsFor("linux", paths, "")
	if len(got) != 2 || got[0] != flat || got[1] != dir {
		t.Fatalf("ConfigSearchPathsFor() = %v", got)
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFor("linux", paths, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ActivePath() != dir {
		t.Fatalf("active path = %q, want %q", cfg.ActivePath(), dir)
	}
	if err := os.WriteFile(flat, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadFor("linux", paths, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ActivePath() != flat {
		t.Fatalf("active path = %q, want %q", cfg.ActivePath(), flat)
	}
}

func TestDefaultPathHonorsXDGConfigHome(t *testing.T) {
	xdg := t.TempDir()
	home := t.TempDir()
	paths := PathsFor("linux", PathEnvironment{HomeDir: home, XDGConfigHome: xdg, TempDir: "/tmp"})
	want := []string{
		filepath.Join(xdg, "waydict.toml"),
		filepath.Join(xdg, "waydict", "config.toml"),
	}
	got := ConfigSearchPathsFor("linux", paths, "")
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("DefaultPaths() = %v, want %v", got, want)
	}
}

func TestDefaultModelsRootHonorsXDGDataHome(t *testing.T) {
	xdg := t.TempDir()
	paths := PathsFor("linux", PathEnvironment{HomeDir: t.TempDir(), XDGDataHome: xdg, TempDir: "/tmp"})
	want := filepath.Join(xdg, "waydict", "models")
	if got := paths.ModelsDir; got != want {
		t.Fatalf("DefaultModelsRoot() = %q, want %q", got, want)
	}
}

func TestDefaultModelsRootFallsBackToHome(t *testing.T) {
	home := t.TempDir()
	paths := PathsFor("linux", PathEnvironment{HomeDir: home, TempDir: "/tmp"})
	want := filepath.Join(home, ".local", "share", "waydict", "models")
	if got := paths.ModelsDir; got != want {
		t.Fatalf("DefaultModelsRoot() = %q, want %q", got, want)
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
	paths := PathsFor("linux", PathEnvironment{HomeDir: home, TempDir: "/tmp", User: "tester"})
	cfg, err := LoadFor("linux", paths, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ASR.NumThreads != 3 {
		t.Fatalf("flat config not loaded: num_threads = %d, want 3", cfg.ASR.NumThreads)
	}
}

func TestLoadAppliesEngineConditionalProviderDefaults(t *testing.T) {
	tests := []struct {
		platform string
		engine   string
		want     string
	}{
		{platform: "linux", engine: asr.EngineAuto, want: ""},
		{platform: "linux", engine: asr.EngineSherpa, want: asr.ProviderCPU},
		{platform: "linux", engine: asr.EngineWhisper, want: asr.ProviderVulkan},
		{platform: "darwin", engine: asr.EngineWhisper, want: ""},
	}
	for _, tc := range tests {
		t.Run(tc.platform+"_"+tc.engine, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte("[asr]\nengine = \""+tc.engine+"\"\n"), 0644); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadFor(tc.platform, testPlatformPaths(t, tc.platform), path)
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
	xdg := t.TempDir()
	paths := PathsFor("linux", PathEnvironment{HomeDir: t.TempDir(), XDGDataHome: xdg, TempDir: "/tmp"})
	cfg := DefaultsFor("linux", paths)
	cfg.ASR.ModelDir = ""
	got := cfg.WhisperModelPath()
	want := filepath.Join(xdg, "waydict", "models", "whisper", "ggml-large-v3-turbo.bin")
	if got != want {
		t.Fatalf("WhisperModelPath() = %q, want %q", got, want)
	}
}

func TestExpandPathRejectsUnknownVariable(t *testing.T) {
	if _, err := ExpandPath("$NOT_ALLOWED/path"); err == nil {
		t.Fatal("expected unknown variable error")
	}
}

func TestLoadMapsLegacyFocusFieldsUnlessGenericFieldsAreExplicit(t *testing.T) {
	tests := []struct {
		name     string
		contents string
		want     Focus
	}{
		{
			name: "legacy",
			contents: `[sway]
require_sway = false
focus_check = false
socket = "/tmp/legacy.sock"
`,
			want: Focus{Enabled: false, Backend: "auto", Policy: "cancel_on_focus_change", Required: false, Socket: "/tmp/legacy.sock"},
		},
		{
			name: "generic override",
			contents: `[sway]
require_sway = false
focus_check = false
socket = "/tmp/legacy.sock"

[focus]
enabled = true
required = true
socket = "/tmp/generic.sock"
`,
			want: Focus{Enabled: true, Backend: "auto", Policy: "cancel_on_focus_change", Required: true, Socket: "/tmp/generic.sock"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(tt.contents), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadFor("linux", testPlatformPaths(t, "linux"), path)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Focus != tt.want {
				t.Fatalf("focus = %#v, want %#v", cfg.Focus, tt.want)
			}
		})
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
			cfg := DefaultsFor("linux", testPlatformPaths(t, "linux"))
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
			name: "hotkey key code",
			edit: func(c *Config) { c.Hotkey.KeyCode = 65536 },
		},
		{
			name: "hotkey symbolic key",
			edit: func(c *Config) { c.Hotkey.Key = "delete" },
		},
		{
			name: "hotkey duplicate modifier",
			edit: func(c *Config) { c.Hotkey.Modifiers = []string{"shift", "shift"} },
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
			cfg := DefaultsFor("linux", testPlatformPaths(t, "linux"))
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

func TestDefaultsForPlatform(t *testing.T) {
	for _, platform := range []string{"linux", "darwin"} {
		t.Run(platform, func(t *testing.T) {
			paths := testPlatformPaths(t, platform)
			cfg := DefaultsFor(platform, paths)
			if cfg.Daemon.Socket != paths.SocketPath || cfg.VAD.Model != paths.SileroModelPath() || cfg.Debug.SaveAudioDir != paths.DebugSegmentsDir {
				t.Fatalf("path defaults = %#v", cfg)
			}
			if platform == "linux" {
				if cfg.Audio.Backend != "pipewire" || cfg.Injection.Engine != "wtype" || cfg.Focus.Backend != "auto" || cfg.Sway.RequireSway {
					t.Fatalf("Linux defaults = %#v", cfg)
				}
				return
			}
			if cfg.Audio.Backend != "auto" || cfg.Audio.Device != "" || cfg.Injection.Engine != "auto" || cfg.Injection.Method != "unicode" {
				t.Fatalf("Darwin backend defaults = %#v", cfg)
			}
			if !cfg.Focus.Enabled || cfg.Focus.Backend != "auto" || cfg.Focus.Policy != "cancel_on_focus_change" {
				t.Fatalf("Darwin focus defaults = %#v", cfg.Focus)
			}
			if !cfg.Hotkey.Enabled || cfg.Hotkey.Key != "space" || cfg.Hotkey.KeyCode != -1 || cfg.Hotkey.Mode != "hold" || len(cfg.Hotkey.Modifiers) != 3 {
				t.Fatalf("Darwin hotkey defaults = %#v", cfg.Hotkey)
			}
		})
	}
}

func TestPathsForPlatform(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	darwin := PathsFor("darwin", PathEnvironment{
		HomeDir:       home,
		UserConfigDir: filepath.Join(home, "Library", "Application Support"),
		UserCacheDir:  filepath.Join(home, "Library", "Caches"),
		UID:           501,
	})
	checks := map[string]string{
		"config": darwin.ConfigFile,
		"models": darwin.ModelsDir,
		"state":  darwin.StateDir,
		"log":    darwin.LogFile,
		"cache":  darwin.CacheDir,
		"socket": darwin.SocketPath,
	}
	wants := map[string]string{
		"config": filepath.Join(home, "Library", "Application Support", "Waydict", "config.toml"),
		"models": filepath.Join(home, "Library", "Application Support", "Waydict", "models"),
		"state":  filepath.Join(home, "Library", "Application Support", "Waydict", "state"),
		"log":    filepath.Join(home, "Library", "Logs", "Waydict", "waydict.log"),
		"cache":  filepath.Join(home, "Library", "Caches", "Waydict"),
		"socket": "/tmp/waydict-501/control.sock",
	}
	for name, got := range checks {
		if got != wants[name] {
			t.Errorf("%s path = %q, want %q", name, got, wants[name])
		}
	}
}

func TestDarwinConfigLookupOrder(t *testing.T) {
	t.Setenv("WAYDICT_CONFIG", "")
	paths := testPlatformPaths(t, "darwin")
	override := filepath.Join(t.TempDir(), "override.toml")
	if got := ConfigSearchPathsFor("darwin", paths, override); len(got) != 1 || got[0] != override {
		t.Fatalf("override lookup = %v", got)
	}
	got := ConfigSearchPathsFor("darwin", paths, "")
	if len(got) != 3 || got[0] != paths.ConfigFile || got[1] != paths.LegacyConfigFiles[0] || got[2] != paths.LegacyConfigFiles[1] {
		t.Fatalf("Darwin lookup = %v", got)
	}
	if err := os.MkdirAll(filepath.Dir(paths.LegacyConfigFiles[0]), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.LegacyConfigFiles[0], []byte("[daemon]\nlog_level = \"debug\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadWithOptions(LoadOptions{Platform: "darwin", Paths: paths})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ActivePath() != paths.LegacyConfigFiles[0] || !cfg.LegacyPathActive() {
		t.Fatalf("active=%q legacy=%t", cfg.ActivePath(), cfg.LegacyPathActive())
	}
}

func TestValidateForCapabilityMatrix(t *testing.T) {
	tests := []struct {
		name     string
		platform string
		edit     func(*Config)
		wantErr  bool
	}{
		{name: "darwin defaults", platform: "darwin"},
		{name: "darwin metal", platform: "darwin", edit: func(c *Config) { c.ASR.Provider = asr.ProviderMetal }},
		{name: "darwin rejects vulkan", platform: "darwin", edit: func(c *Config) { c.ASR.Provider = asr.ProviderVulkan }, wantErr: true},
		{name: "darwin rejects pipewire", platform: "darwin", edit: func(c *Config) { c.Audio.Backend = "pipewire" }, wantErr: true},
		{name: "linux accepts vulkan", platform: "linux", edit: func(c *Config) { c.ASR.Provider = asr.ProviderVulkan }},
		{name: "linux rejects quartz", platform: "linux", edit: func(c *Config) { c.Injection.Engine = "quartz" }, wantErr: true},
		{name: "focus none disabled", platform: "darwin", edit: func(c *Config) { c.Focus.Enabled = false; c.Focus.Backend = "none" }},
		{name: "focus none enabled", platform: "darwin", edit: func(c *Config) { c.Focus.Backend = "none" }, wantErr: true},
		{name: "linux hotkey enabled", platform: "linux", edit: func(c *Config) { c.Hotkey.Enabled = true }, wantErr: true},
		{name: "sway optional", platform: "linux", edit: func(c *Config) { c.Sway.RequireSway = false }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultsFor(tc.platform, testPlatformPaths(t, tc.platform))
			cfg.ASR.NumThreads = 1
			if tc.edit != nil {
				tc.edit(&cfg)
			}
			err := cfg.ValidateFor(CapabilitySetFor(tc.platform))
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateFor() error = %v, wantErr %t", err, tc.wantErr)
			}
		})
	}
}

func TestLoadMigratesLegacyAliases(t *testing.T) {
	tests := []struct {
		name     string
		platform string
		body     string
		check    func(*testing.T, Config)
	}{
		{name: "audio target Linux", platform: "linux", body: "[audio]\ntarget_object = \"source-1\"\n", check: func(t *testing.T, cfg Config) {
			if cfg.Audio.Device != "source-1" {
				t.Fatalf("device = %q", cfg.Audio.Device)
			}
		}},
		{name: "audio target Darwin ignored", platform: "darwin", body: "[audio]\ntarget_object = \"source-1\"\n", check: func(t *testing.T, cfg Config) {
			if cfg.Audio.Device != "" || len(cfg.MigrationWarnings()) == 0 {
				t.Fatalf("device=%q warnings=%v", cfg.Audio.Device, cfg.MigrationWarnings())
			}
		}},
		{name: "injection focus policy", platform: "linux", body: "[injection]\nfocus_policy = \"warn_and_type\"\n", check: func(t *testing.T, cfg Config) {
			if cfg.Focus.Policy != "warn_and_type" {
				t.Fatalf("policy = %q", cfg.Focus.Policy)
			}
		}},
		{name: "sway focus check", platform: "linux", body: "[sway]\nfocus_check = false\n", check: func(t *testing.T, cfg Config) {
			if cfg.Focus.Enabled {
				t.Fatal("focus remained enabled")
			}
		}},
		{name: "sway required", platform: "linux", body: "[sway]\nrequire_sway = true\n", check: func(t *testing.T, cfg Config) {
			if cfg.Focus.Backend != "sway" {
				t.Fatalf("backend = %q", cfg.Focus.Backend)
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(tc.body), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadFor(tc.platform, testPlatformPaths(t, tc.platform), path)
			if err != nil {
				t.Fatal(err)
			}
			tc.check(t, cfg)
		})
	}
}

func TestMigrationPrecedence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	body := `[audio]
device = "preferred"
target_object = "legacy"

[injection]
focus_policy = "warn_and_type"

[focus]
enabled = true
policy = "type_current"
`
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFor("linux", testPlatformPaths(t, "linux"), path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Audio.Device != "preferred" || cfg.Focus.Policy != "type_current" {
		t.Fatalf("migration overrode generic fields: audio=%q policy=%q", cfg.Audio.Device, cfg.Focus.Policy)
	}
	if !cfg.IsExplicit("audio.device") || !cfg.IsExplicit("focus.policy") {
		t.Fatal("explicit TOML fields were not recorded")
	}
}

func TestEnsureConfigForEditing(t *testing.T) {
	paths := testPlatformPaths(t, "darwin")
	paths.ConfigDir = filepath.Join(t.TempDir(), "Waydict")
	paths.ConfigFile = filepath.Join(paths.ConfigDir, "config.toml")
	path, created, err := EnsureConfigForEditing("", paths, SampleConfigFor("darwin"))
	if err != nil {
		t.Fatal(err)
	}
	if path != paths.ConfigFile || !created {
		t.Fatalf("path=%q created=%t", path, created)
	}
	for _, item := range []struct {
		path string
		mode os.FileMode
	}{{paths.ConfigDir, 0700}, {paths.ConfigFile, 0600}} {
		info, err := os.Stat(item.path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != item.mode {
			t.Fatalf("%s mode = %o", item.path, info.Mode().Perm())
		}
	}
	if err := os.WriteFile(paths.ConfigFile, []byte("broken = ["), 0600); err != nil {
		t.Fatal(err)
	}
	_, created, err = EnsureConfigForEditing("", paths, SampleConfigFor("darwin"))
	if err != nil || created {
		t.Fatalf("existing config: created=%t err=%v", created, err)
	}
	contents, err := os.ReadFile(paths.ConfigFile)
	if err != nil || string(contents) != "broken = [" {
		t.Fatalf("existing config was overwritten: %q err=%v", contents, err)
	}
}

func TestMacOSSampleConfigGolden(t *testing.T) {
	want, err := os.ReadFile(filepath.Join("..", "..", "testdata", "sample-config-macos.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(SampleConfigFor("darwin")) != string(want) {
		t.Fatal("embedded macOS sample differs from testdata/sample-config-macos.toml")
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, want, 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFor("darwin", testPlatformPaths(t, "darwin"), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.ValidateFor(CapabilitySetFor("darwin")); err != nil {
		t.Fatal(err)
	}
}

func testPlatformPaths(t *testing.T, platform string) PlatformPaths {
	t.Helper()
	home := t.TempDir()
	return PathsFor(platform, PathEnvironment{
		HomeDir:       home,
		UserConfigDir: filepath.Join(home, "Library", "Application Support"),
		UserCacheDir:  filepath.Join(home, "Library", "Caches"),
		TempDir:       "/tmp",
		User:          "tester",
		UID:           501,
		SwaySocket:    "/tmp/sway.sock",
	})
}
