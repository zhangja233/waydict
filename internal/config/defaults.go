package config

import (
	"os"
	"runtime"

	"waydict/internal/asr"
)

const DefaultModelName = "parakeet-unified-en-0.6b-fp32"

func DefaultPaths() []string {
	paths := CurrentPlatformPaths()
	override := ""
	if runtime.GOOS == "darwin" {
		override = os.Getenv("WAYDICT_CONFIG")
	}
	return ConfigSearchPathsFor(runtime.GOOS, paths, override)
}

func DefaultPath() string {
	paths := DefaultPaths()
	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	if len(paths) == 0 {
		return ""
	}
	return paths[0]
}

func DefaultModelsRoot() string {
	return CurrentPlatformPaths().ModelsDir
}

func Defaults() Config {
	return DefaultsFor(runtime.GOOS, CurrentPlatformPaths())
}

// DefaultsFor has no process or filesystem dependencies.
func DefaultsFor(platform string, paths PlatformPaths) Config {
	cfg := Config{
		Daemon: Daemon{
			Socket:                      paths.SocketPath,
			PreloadModel:                true,
			AutoStopAfterSilenceSeconds: 120,
			RedactTranscriptsInLogs:     true,
			LogLevel:                    "info",
		},
		Audio: Audio{
			Backend:     "pipewire",
			SampleRate:  16000,
			Channels:    1,
			Format:      "f32le",
			QuantumMS:   20,
			RingSeconds: 8,
			StartPaused: true,
		},
		VAD: VAD{
			Engine:            "silero",
			Model:             paths.SileroModelPath(),
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
			ModelDir:       paths.ParakeetModelPath(),
			Encoder:        "encoder.onnx",
			Decoder:        "decoder.onnx",
			Joiner:         "joiner.onnx",
			Tokens:         "tokens.txt",
		},
		Injection: Injection{
			Engine:      "wtype",
			Method:      "unicode",
			WtypePath:   "wtype",
			KeyDelayMS:  1,
			TimeoutMS:   10000,
			AppendSpace: true,
		},
		Focus: Focus{
			Enabled:  true,
			Backend:  "auto",
			Policy:   "cancel_on_focus_change",
			Required: false,
			Socket:   paths.SwaySocket,
		},
		Hotkey: Hotkey{
			Enabled: false,
			Key:     "space",
			KeyCode: -1,
			Mode:    "hold",
		},
		PostProcess: PostProcess{
			TrimLeading:              true,
			CollapseSpaces:           true,
			FixPunctuationSpacing:    true,
			SpokenFormattingCommands: false,
			SmartCase:                true,
		},
		Sway: Sway{
			RequireSway: false,
			Socket:      paths.SwaySocket,
			FocusCheck:  true,
		},
		Debug: Debug{
			SaveAudioSegments: false,
			SaveAudioDir:      paths.DebugSegmentsDir,
			LogTranscripts:    false,
		},
	}
	if platform == "darwin" {
		cfg.Audio.Backend = "auto"
		cfg.Injection.Engine = "auto"
		cfg.Injection.WtypePath = ""
		cfg.Focus.Backend = "auto"
		cfg.Focus.Socket = ""
		cfg.Hotkey.Enabled = true
		cfg.Hotkey.Modifiers = []string{"control", "shift", "command"}
		cfg.Sway = Sway{}
	}
	cfg.source.platform = platform
	cfg.source.paths = paths
	return cfg
}
