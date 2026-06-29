package config

import (
	"os"
	"path/filepath"
)

const (
	DefaultModelName = "parakeet-tdt-0.6b-v3-int8"
)

func DefaultPath() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "waydict", "config.toml")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "waydict", "config.toml")
}

func Defaults() Config {
	home, _ := os.UserHomeDir()
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeDir == "" {
		runtimeDir = filepath.Join(os.TempDir(), "waydict-"+userName())
	}
	modelRoot := filepath.Join(home, ".local", "share", "waydict", "models")
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
			Engine:         "sherpa-onnx",
			Provider:       "cpu",
			ModelType:      "nemo_transducer",
			DecodingMethod: "greedy_search",
			NumThreads:     4,
			MaxActivePaths: 4,
			BlankPenalty:   0,
			ModelDir:       filepath.Join(modelRoot, DefaultModelName),
			Encoder:        "encoder.int8.onnx",
			Decoder:        "decoder.int8.onnx",
			Joiner:         "joiner.int8.onnx",
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
		PostProcess: PostProcess{
			TrimLeading:              true,
			CollapseSpaces:           true,
			FixPunctuationSpacing:    true,
			SpokenFormattingCommands: false,
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
