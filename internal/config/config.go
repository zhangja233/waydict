package config

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/BurntSushi/toml"

	"waydict/internal/asr"
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
	Engine         string   `toml:"engine"`
	Provider       string   `toml:"provider"`
	WhisperModel   string   `toml:"whisper_model"`
	Vocabulary     []string `toml:"vocabulary"`
	GPUDevice      int      `toml:"gpu_device"`
	ModelType      string   `toml:"model_type"`
	DecodingMethod string   `toml:"decoding_method"`
	NumThreads     int      `toml:"num_threads"`
	MaxActivePaths int      `toml:"max_active_paths"`
	BlankPenalty   float32  `toml:"blank_penalty"`
	ModelDir       string   `toml:"model_dir"`
	Encoder        string   `toml:"encoder"`
	Decoder        string   `toml:"decoder"`
	Joiner         string   `toml:"joiner"`
	Tokens         string   `toml:"tokens"`
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
	TrimLeading              bool              `toml:"trim_leading"`
	CollapseSpaces           bool              `toml:"collapse_spaces"`
	FixPunctuationSpacing    bool              `toml:"fix_punctuation_spacing"`
	SpokenFormattingCommands bool              `toml:"spoken_formatting_commands"`
	SmartCase                bool              `toml:"smart_case"`
	Replacements             map[string]string `toml:"replacements"`
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
	cfg.applyASRDefaults()
	if err := cfg.Expand(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c *Config) applyASRDefaults() {
	if c.ASR.Provider != "" {
		return
	}
	switch c.ASR.Engine {
	case asr.EngineSherpa:
		c.ASR.Provider = asr.ProviderCPU
	case asr.EngineWhisper:
		c.ASR.Provider = asr.ProviderVulkan
	}
}

func (c *Config) Expand() error {
	var err error
	paths := []*string{
		&c.Daemon.Socket,
		&c.VAD.Model,
		&c.ASR.ModelDir,
		&c.ASR.WhisperModel,
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

func (c Config) WhisperModelPath() string {
	return filepath.Join(DefaultModelsRoot(), "whisper", c.ASR.WhisperModel+".bin")
}

func WhisperInitialPrompt(vocabulary []string) string {
	if len(vocabulary) == 0 {
		return ""
	}
	return "Vocabulary: " + strings.Join(vocabulary, ", ") + "."
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.Daemon.Socket) == "" {
		return fmt.Errorf("daemon.socket must not be empty")
	}
	if !supportedLogLevel(c.Daemon.LogLevel) {
		return fmt.Errorf("daemon.log_level must be debug, info, warn, or error")
	}
	if c.Audio.Backend != "pipewire" {
		return fmt.Errorf("audio.backend must equal pipewire")
	}
	if c.Audio.Format != "f32le" {
		return fmt.Errorf("audio.format must equal f32le")
	}
	if c.Audio.QuantumMS <= 0 {
		return fmt.Errorf("audio.quantum_ms must be positive")
	}
	if err := c.ValidateASR(); err != nil {
		return err
	}
	if err := c.ValidatePostProcess(); err != nil {
		return err
	}
	if c.Injection.Engine != "wtype" {
		return fmt.Errorf("injection.engine must equal wtype")
	}
	if strings.TrimSpace(c.Injection.WtypePath) == "" {
		return fmt.Errorf("injection.wtype_path must not be empty")
	}
	if !c.Sway.RequireSway {
		return fmt.Errorf("sway.require_sway must be true")
	}
	if c.Daemon.AutoStopAfterSilenceSeconds < 0 {
		return fmt.Errorf("daemon.auto_stop_after_silence_seconds must not be negative")
	}
	if c.VAD.Engine != "silero" && c.VAD.Engine != "energy" {
		return fmt.Errorf("vad.engine must be silero or energy")
	}
	if c.VAD.Engine == "silero" && strings.TrimSpace(c.VAD.Model) == "" {
		return fmt.Errorf("vad.model must not be empty when vad.engine is silero")
	}
	if c.VAD.WindowSize <= 0 {
		return fmt.Errorf("vad.window_size must be positive")
	}
	if c.VAD.Threshold <= 0 || c.VAD.Threshold > 1 {
		return fmt.Errorf("vad.threshold must be between 0 and 1")
	}
	if c.VAD.NegativeThreshold < 0 || c.VAD.NegativeThreshold > c.VAD.Threshold {
		return fmt.Errorf("vad.negative_threshold must be between 0 and vad.threshold")
	}
	if c.VAD.MinSpeechMS <= 0 {
		return fmt.Errorf("vad.min_speech_ms must be positive")
	}
	if c.VAD.MinSilenceMS <= 0 {
		return fmt.Errorf("vad.min_silence_ms must be positive")
	}
	if c.VAD.SpeechPadMS < 0 {
		return fmt.Errorf("vad.speech_pad_ms must not be negative")
	}
	if c.VAD.PreRollMS < 0 {
		return fmt.Errorf("vad.pre_roll_ms must not be negative")
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
	if c.Injection.KeyDelayMS < 0 {
		return fmt.Errorf("injection.key_delay_ms must not be negative")
	}
	if c.Injection.TimeoutMS <= 0 {
		return fmt.Errorf("injection.timeout_ms must be positive")
	}
	if c.Injection.FocusPolicy != "cancel_on_focus_change" && c.Injection.FocusPolicy != "warn_and_type" && c.Injection.FocusPolicy != "type_current" {
		return fmt.Errorf("unsupported injection.focus_policy %q", c.Injection.FocusPolicy)
	}
	if c.Debug.SaveAudioSegments && strings.TrimSpace(c.Debug.SaveAudioDir) == "" {
		return fmt.Errorf("debug.save_audio_dir must not be empty when debug.save_audio_segments is true")
	}
	return nil
}

func (c Config) ValidatePostProcess() error {
	for key := range c.PostProcess.Replacements {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("postprocess.replacements keys must not be empty")
		}
		runes := []rune(key)
		if !isASCIIWord(runes[0]) || !isASCIIWord(runes[len(runes)-1]) {
			return fmt.Errorf("postprocess.replacements key %q must start and end with an ASCII word character [A-Za-z0-9_]", key)
		}
	}
	return nil
}

func isASCIIWord(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_'
}

func (c Config) ValidateASR() error {
	for i, word := range c.ASR.Vocabulary {
		if strings.TrimSpace(word) == "" {
			return fmt.Errorf("asr.vocabulary[%d] must not be empty", i)
		}
	}
	switch c.ASR.Engine {
	case asr.EngineSherpa:
		return c.validateSherpaASR()
	case asr.EngineWhisper:
		provider := c.ASR.Provider
		if provider == "" {
			provider = asr.ProviderVulkan
		}
		if provider != asr.ProviderCPU && provider != asr.ProviderVulkan {
			return fmt.Errorf("asr.provider must be cpu or vulkan for whisper-cpp")
		}
		if err := validateWhisperModelName(c.ASR.WhisperModel); err != nil {
			return err
		}
		if c.ASR.GPUDevice < 0 {
			return fmt.Errorf("asr.gpu_device must not be negative")
		}
		return nil
	case asr.EngineAuto:
		if c.ASR.Provider != "" && c.ASR.Provider != asr.ProviderCPU && c.ASR.Provider != asr.ProviderVulkan {
			return fmt.Errorf("asr.provider must be empty, cpu, or vulkan for auto")
		}
		if c.ASR.GPUDevice < 0 {
			return fmt.Errorf("asr.gpu_device must not be negative")
		}
		if c.ASR.WhisperModel != "" {
			if err := validateWhisperModelName(c.ASR.WhisperModel); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("asr.engine must be auto, sherpa-onnx, or whisper-cpp")
	}
}

// Bare file stem only: the name is joined under the models root, so any
// separator component would escape it.
func validateWhisperModelName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("asr.whisper_model must not be empty for whisper-cpp")
	}
	if name != filepath.Base(name) || name == "." || name == ".." {
		return fmt.Errorf("asr.whisper_model must be a bare model name")
	}
	return nil
}

func (c Config) validateSherpaASR() error {
	provider := c.ASR.Provider
	if provider == "" {
		provider = asr.ProviderCPU
	}
	if provider != asr.ProviderCPU {
		return fmt.Errorf("asr.provider must equal cpu for sherpa-onnx")
	}
	if c.ASR.ModelType != "nemo_transducer" {
		return fmt.Errorf("asr.model_type must equal nemo_transducer")
	}
	if c.ASR.DecodingMethod != "greedy_search" {
		return fmt.Errorf("asr.decoding_method must equal greedy_search")
	}
	if strings.TrimSpace(c.ASR.ModelDir) == "" {
		return fmt.Errorf("asr.model_dir must not be empty")
	}
	modelFiles := []struct {
		name  string
		value string
	}{
		{name: "asr.encoder", value: c.ASR.Encoder},
		{name: "asr.decoder", value: c.ASR.Decoder},
		{name: "asr.joiner", value: c.ASR.Joiner},
		{name: "asr.tokens", value: c.ASR.Tokens},
	}
	for _, file := range modelFiles {
		if err := validateModelFile(file.name, file.value); err != nil {
			return err
		}
	}
	if c.ASR.MaxActivePaths < 1 {
		return fmt.Errorf("asr.max_active_paths must be positive")
	}
	if math.IsNaN(float64(c.ASR.BlankPenalty)) || math.IsInf(float64(c.ASR.BlankPenalty), 0) {
		return fmt.Errorf("asr.blank_penalty must be finite")
	}
	if c.ASR.NumThreads < 1 || c.ASR.NumThreads > runtime.NumCPU() {
		return fmt.Errorf("asr.num_threads must be between 1 and %d", runtime.NumCPU())
	}
	return nil
}

func supportedLogLevel(level string) bool {
	switch level {
	case "debug", "info", "warn", "error":
		return true
	default:
		return false
	}
}

func validateModelFile(name, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s must not be empty", name)
	}
	if filepath.IsAbs(value) {
		return fmt.Errorf("%s must be relative to asr.model_dir", name)
	}
	clean := filepath.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%s must stay within asr.model_dir", name)
	}
	return nil
}

func (c Config) ValidateModelReadable() error {
	if c.ASR.Engine != asr.EngineSherpa {
		return nil
	}
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
