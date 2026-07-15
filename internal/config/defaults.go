package config

import (
	"os"
	"path/filepath"

	"waydict/internal/asr"
)

const (
	DefaultModelName = "parakeet-unified-en-0.6b-fp32"
)

// DefaultPaths lists the config locations searched when --config is not given,
// in precedence order: the flat waydict.toml wins over the directory-form
// waydict/config.toml. XDG_CONFIG_HOME overrides ~/.config for both.
func DefaultPaths() []string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return []string{
		filepath.Join(dir, "waydict.toml"),
		filepath.Join(dir, "waydict", "config.toml"),
	}
}

// DefaultPath returns the first config location that exists, or the preferred
// one (flat waydict.toml) when none do.
func DefaultPath() string {
	paths := DefaultPaths()
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return paths[0]
}

func DefaultModelsRoot() string {
	dir := os.Getenv("XDG_DATA_HOME")
	if dir != "" {
		return filepath.Join(dir, "waydict", "models")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "waydict", "models")
}

func Defaults() Config {
	home, _ := os.UserHomeDir()
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeDir == "" {
		runtimeDir = filepath.Join(os.TempDir(), "waydict-"+userName())
	}
	modelRoot := DefaultModelsRoot()
	stateRoot := filepath.Join(home, ".local", "state", "waydict")
	return Config{
		Daemon: Daemon{
			Socket:                      filepath.Join(runtimeDir, "waydict", "waydict.sock"),
			PreloadModel:                true,
			AutoStopAfterSilenceSeconds: 120,
			RedactTranscriptsInLogs:     true,
			LogLevel:                    "info",
		},
		Audio: Audio{
			Backend:      "pipewire",
			SampleRate:   16000,
			Channels:     1,
			Format:       "f32le",
			QuantumMS:    20,
			RingSeconds:  8,
			TargetObject: "",
			StartPaused:  true,
		},
		VAD: VAD{
			Engine:            "silero",
			Model:             filepath.Join(modelRoot, "silero_vad.onnx"),
			WindowSize:        512,
			Threshold:         0.35,
			NegativeThreshold: 0.15,
			MinSpeechMS:       200,
			MinSilenceMS:      500,
			SpeechPadMS:       300,
			PreRollMS:         400,
			MaxSpeechSeconds:  20,
		},
		ASR: ASR{
			Engine:         asr.EngineAuto,
			Provider:       "",
			ModelType:      "nemo_transducer",
			DecodingMethod: "greedy_search",
			NumThreads:     4,
			MaxActivePaths: 4,
			BlankPenalty:   0,
			WhisperModel:   "ggml-large-v3-turbo",
			GPUDevice:      0,
			ModelDir:       filepath.Join(modelRoot, DefaultModelName),
			Encoder:        "encoder.onnx",
			Decoder:        "decoder.onnx",
			Joiner:         "joiner.onnx",
			Tokens:         "tokens.txt",
		},
		Injection: Injection{
			Engine:      "wtype",
			WtypePath:   "wtype",
			KeyDelayMS:  1,
			TimeoutMS:   10000,
			AppendSpace: true,
			FocusPolicy: "cancel_on_focus_change",
		},
		Focus: Focus{
			Enabled:  true,
			Backend:  "sway",
			Required: true,
			Socket:   os.Getenv("SWAYSOCK"),
		},
		PostProcess: PostProcess{
			TrimLeading:              true,
			CollapseSpaces:           true,
			FixPunctuationSpacing:    true,
			SpokenFormattingCommands: false,
			SmartCase:                true,
		},
		Sway: Sway{
			RequireSway: true,
			Socket:      os.Getenv("SWAYSOCK"),
			FocusCheck:  true,
		},
		Debug: Debug{
			SaveAudioSegments: false,
			SaveAudioDir:      filepath.Join(stateRoot, "segments"),
			LogTranscripts:    false,
		},
	}
}

func userName() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return "user"
}
