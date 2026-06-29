package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Daemon      Daemon      `toml:"daemon"`
	Audio       Audio       `toml:"audio"`
	VAD         VAD         `toml:"vad"`
	ASR         ASR         `toml:"asr"`
	Injection   Injection   `toml:"injection"`
	PostProcess PostProcess `toml:"postprocess"`
	Sway        Sway        `toml:"sway"`
	Debug       Debug       `toml:"debug"`
}

type Daemon struct {
	Socket                      string `toml:"socket"`
	PreloadModel                bool   `toml:"preload_model"`
	AutoStopAfterSilenceSeconds int    `toml:"auto_stop_after_silence_seconds"`
	RedactTranscriptsInLogs     bool   `toml:"redact_transcripts_in_logs"`
	LogLevel                    string `toml:"log_level"`
}

type Audio struct {
	Backend      string `toml:"backend"`
	SampleRate   int    `toml:"sample_rate"`
	Channels     int    `toml:"channels"`
	Format       string `toml:"format"`
	QuantumMS    int    `toml:"quantum_ms"`
	RingSeconds  int    `toml:"ring_seconds"`
	TargetObject string `toml:"target_object"`
	StartPaused  bool   `toml:"start_paused"`
}

type VAD struct {
	Engine            string  `toml:"engine"`
	Model             string  `toml:"model"`
	WindowSize        int     `toml:"window_size"`
	Threshold         float64 `toml:"threshold"`
	NegativeThreshold float64 `toml:"negative_threshold"`
	MinSpeechMS       int     `toml:"min_speech_ms"`
	MinSilenceMS      int     `toml:"min_silence_ms"`
	SpeechPadMS       int     `toml:"speech_pad_ms"`
	PreRollMS         int     `toml:"pre_roll_ms"`
	MaxSpeechSeconds  int     `toml:"max_speech_seconds"`
}

type ASR struct {
	Engine         string  `toml:"engine"`
	Provider       string  `toml:"provider"`
	ModelType      string  `toml:"model_type"`
	DecodingMethod string  `toml:"decoding_method"`
	NumThreads     int     `toml:"num_threads"`
	MaxActivePaths int     `toml:"max_active_paths"`
	BlankPenalty   float32 `toml:"blank_penalty"`
	ModelDir       string  `toml:"model_dir"`
	Encoder        string  `toml:"encoder"`
	Decoder        string  `toml:"decoder"`
	Joiner         string  `toml:"joiner"`
	Tokens         string  `toml:"tokens"`
}

type Injection struct {
	Engine      string `toml:"engine"`
	WtypePath   string `toml:"wtype_path"`
	KeyDelayMS  int    `toml:"key_delay_ms"`
	TimeoutMS   int    `toml:"timeout_ms"`
	AppendSpace bool   `toml:"append_space"`
	FocusPolicy string `toml:"focus_policy"`
}

type PostProcess struct {
	TrimLeading              bool `toml:"trim_leading"`
	CollapseSpaces           bool `toml:"collapse_spaces"`
	FixPunctuationSpacing    bool `toml:"fix_punctuation_spacing"`
	SpokenFormattingCommands bool `toml:"spoken_formatting_commands"`
}

type Sway struct {
	RequireSway bool   `toml:"require_sway"`
	Socket      string `toml:"socket"`
	FocusCheck  bool   `toml:"focus_check"`
}

type Debug struct {
	SaveAudioSegments bool   `toml:"save_audio_segments"`
	SaveAudioDir      string `toml:"save_audio_dir"`
	LogTranscripts    bool   `toml:"log_transcripts"`
}

func Load(path string) (Config, error) {
	cfg := Defaults()
	if path == "" {
		path = DefaultPath()
	}
	if _, err := os.Stat(path); err == nil {
		if _, err := toml.DecodeFile(path, &cfg); err != nil {
			return Config{}, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return Config{}, err
	}
	if err := cfg.Expand(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c *Config) Expand() error {
	var err error
	paths := []*string{
		&c.Daemon.Socket,
		&c.VAD.Model,
		&c.ASR.ModelDir,
		&c.Sway.Socket,
		&c.Debug.SaveAudioDir,
	}
	for _, p := range paths {
		*p, err = ExpandPath(*p)
		if err != nil {
			return err
		}
	}
	return nil
}

func ExpandPath(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if strings.HasPrefix(value, "~/") || value == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if value == "~" {
			value = home
		} else {
			value = filepath.Join(home, value[2:])
		}
	}
	known := map[string]string{
		"HOME":            homeOrEmpty(),
		"USER":            os.Getenv("USER"),
		"SCRATCH":         os.Getenv("SCRATCH"),
		"XDG_RUNTIME_DIR": os.Getenv("XDG_RUNTIME_DIR"),
		"XDG_CONFIG_HOME": os.Getenv("XDG_CONFIG_HOME"),
		"XDG_DATA_HOME":   os.Getenv("XDG_DATA_HOME"),
		"XDG_STATE_HOME":  os.Getenv("XDG_STATE_HOME"),
		"SWAYSOCK":        os.Getenv("SWAYSOCK"),
	}
	missing := ""
	out := os.Expand(value, func(name string) string {
		v, ok := known[name]
		if !ok {
			missing = name
			return ""
		}
		return v
	})
	if missing != "" {
		return "", fmt.Errorf("unknown environment variable %q in path", missing)
	}
	return out, nil
}

func homeOrEmpty() string {
	home, _ := os.UserHomeDir()
	return home
}

func (c Config) EncoderPath() string {
	return filepath.Join(c.ASR.ModelDir, c.ASR.Encoder)
}

func (c Config) DecoderPath() string {
	return filepath.Join(c.ASR.ModelDir, c.ASR.Decoder)
}

func (c Config) JoinerPath() string {
	return filepath.Join(c.ASR.ModelDir, c.ASR.Joiner)
}

func (c Config) TokensPath() string {
	return filepath.Join(c.ASR.ModelDir, c.ASR.Tokens)
}

func (c Config) Validate() error {
	if c.Audio.Backend != "pipewire" {
		return fmt.Errorf("audio.backend must equal pipewire")
	}
	if c.ASR.Provider != "cpu" {
		return fmt.Errorf("asr.provider must equal cpu")
	}
	if c.ASR.Engine != "sherpa-onnx" {
		return fmt.Errorf("asr.engine must equal sherpa-onnx")
	}
	if c.Injection.Engine != "wtype" {
		return fmt.Errorf("injection.engine must equal wtype")
	}
	if !c.Sway.RequireSway {
		return fmt.Errorf("sway.require_sway must be true")
	}
	if c.ASR.NumThreads < 1 || c.ASR.NumThreads > runtime.NumCPU() {
		return fmt.Errorf("asr.num_threads must be between 1 and %d", runtime.NumCPU())
	}
	if c.VAD.MaxSpeechSeconds < 3 || c.VAD.MaxSpeechSeconds > 60 {
		return fmt.Errorf("vad.max_speech_seconds must be between 3 and 60")
	}
	minRing := 5
	if v := c.VAD.PreRollMS/1000 + 2; v > minRing {
		minRing = v
	}
	if c.Audio.RingSeconds < minRing {
		return fmt.Errorf("audio.ring_seconds must be at least %d", minRing)
	}
	if c.Audio.SampleRate != 16000 {
		return fmt.Errorf("audio.sample_rate must equal 16000")
	}
	if c.Audio.Channels != 1 {
		return fmt.Errorf("audio.channels must equal 1")
	}
	if c.Injection.FocusPolicy != "cancel_on_focus_change" && c.Injection.FocusPolicy != "warn_and_type" && c.Injection.FocusPolicy != "type_current" {
		return fmt.Errorf("unsupported injection.focus_policy %q", c.Injection.FocusPolicy)
	}
	return nil
}

func (c Config) ValidateModelReadable() error {
	for _, p := range []string{c.EncoderPath(), c.DecoderPath(), c.JoinerPath(), c.TokensPath()} {
		if err := readableFile(p); err != nil {
			return err
		}
	}
	return nil
}

func readableFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	return f.Close()
}
