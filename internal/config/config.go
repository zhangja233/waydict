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
	"waydict/internal/hotkey"
)

type Config struct {
	Daemon      Daemon      `toml:"daemon"`
	Audio       Audio       `toml:"audio"`
	VAD         VAD         `toml:"vad"`
	ASR         ASR         `toml:"asr"`
	Injection   Injection   `toml:"injection"`
	Focus       Focus       `toml:"focus"`
	Hotkey      Hotkey      `toml:"hotkey"`
	PostProcess PostProcess `toml:"postprocess"`
	Sway        Sway        `toml:"sway"`
	Debug       Debug       `toml:"debug"`

	source configSource
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
	Device       string `toml:"device"`
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
	WhisperModel   string  `toml:"whisper_model"`
	GPUDevice      int     `toml:"gpu_device"`
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
	Method      string `toml:"method"`
	WtypePath   string `toml:"wtype_path"`
	KeyDelayMS  int    `toml:"key_delay_ms"`
	TimeoutMS   int    `toml:"timeout_ms"`
	AppendSpace bool   `toml:"append_space"`
	FocusPolicy string `toml:"focus_policy"`
}

type Focus struct {
	Enabled bool   `toml:"enabled"`
	Backend string `toml:"backend"`
	Policy  string `toml:"policy"`
	// Required and Socket are retained for PR1/Linux compatibility.
	Required bool   `toml:"required"`
	Socket   string `toml:"socket"`
}

type Hotkey struct {
	Enabled   bool     `toml:"enabled"`
	Key       string   `toml:"key"`
	KeyCode   int      `toml:"key_code"`
	Modifiers []string `toml:"modifiers"`
	Mode      string   `toml:"mode"`
}

type PostProcess struct {
	TrimLeading              bool `toml:"trim_leading"`
	CollapseSpaces           bool `toml:"collapse_spaces"`
	FixPunctuationSpacing    bool `toml:"fix_punctuation_spacing"`
	SpokenFormattingCommands bool `toml:"spoken_formatting_commands"`
	SmartCase                bool `toml:"smart_case"`
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

type configSource struct {
	platform string
	paths    PlatformPaths
	path     string
	legacy   bool
	warnings []string
	explicit map[string]bool
}

type LoadOptions struct {
	Path           string
	Platform       string
	Paths          PlatformPaths
	ConfigOverride string
}

func Load(path string) (Config, error) {
	return LoadWithOptions(LoadOptions{Path: path})
}

func LoadFor(platform string, paths PlatformPaths, path string) (Config, error) {
	return LoadWithOptions(LoadOptions{Path: path, Platform: platform, Paths: paths})
}

func LoadWithOptions(opts LoadOptions) (Config, error) {
	platform := opts.Platform
	if platform == "" {
		platform = runtime.GOOS
	}
	paths := opts.Paths
	if paths.ConfigFile == "" {
		paths = CurrentPlatformPaths()
	}
	cfg := DefaultsFor(platform, paths)
	path := opts.Path
	if path == "" {
		override := opts.ConfigOverride
		if override == "" && platform == "darwin" {
			override = os.Getenv("WAYDICT_CONFIG")
		}
		for _, candidate := range ConfigSearchPathsFor(platform, paths, override) {
			if _, err := os.Stat(candidate); err == nil {
				path = candidate
				break
			} else if !errors.Is(err, os.ErrNotExist) {
				return Config{}, err
			}
		}
	}
	if path != "" {
		if _, err := os.Stat(path); err == nil {
			metadata, err := toml.DecodeFile(path, &cfg)
			if err != nil {
				return Config{}, err
			}
			cfg.source.path = path
			cfg.source.legacy = isLegacyConfigPath(path, paths)
			cfg.captureExplicitFields(metadata)
			cfg.applyLegacyConfig(platform, metadata)
		} else if !errors.Is(err, os.ErrNotExist) {
			return Config{}, err
		}
	}
	cfg.applyASRDefaults(platform)
	if err := cfg.Expand(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c *Config) captureExplicitFields(metadata toml.MetaData) {
	c.source.explicit = make(map[string]bool)
	for _, key := range metadata.Keys() {
		c.source.explicit[key.String()] = true
	}
}

func (c *Config) applyLegacyConfig(platform string, metadata toml.MetaData) {
	if metadata.IsDefined("audio", "target_object") && c.Audio.TargetObject != "" {
		switch {
		case platform == "linux" && (!metadata.IsDefined("audio", "device") || c.Audio.Device == ""):
			c.Audio.Device = c.Audio.TargetObject
			c.addMigrationWarning("audio.target_object was mapped to audio.device")
		case platform == "darwin" && !metadata.IsDefined("audio", "device"):
			c.addMigrationWarning("audio.target_object is Linux-only and was ignored on macOS")
		}
	}
	if metadata.IsDefined("injection", "focus_policy") && !metadata.IsDefined("focus", "policy") {
		c.Focus.Policy = c.Injection.FocusPolicy
		c.addMigrationWarning("injection.focus_policy was mapped to focus.policy")
	}
	if metadata.IsDefined("sway", "focus_check") && !c.Sway.FocusCheck && !metadata.IsDefined("focus") {
		c.Focus.Enabled = false
		c.addMigrationWarning("sway.focus_check=false was mapped to focus.enabled=false")
	}
	if platform == "linux" && metadata.IsDefined("sway", "require_sway") && c.Sway.RequireSway && !metadata.IsDefined("focus", "backend") {
		c.Focus.Backend = "sway"
		c.addMigrationWarning("sway.require_sway=true was mapped to focus.backend=\"sway\"")
	}
	if platform == "linux" && !metadata.IsDefined("focus", "required") {
		c.Focus.Required = c.Sway.RequireSway
	}
	if platform == "linux" && !metadata.IsDefined("focus", "socket") && metadata.IsDefined("sway", "socket") {
		c.Focus.Socket = c.Sway.Socket
	}
}

func (c *Config) addMigrationWarning(message string) {
	c.source.warnings = append(c.source.warnings, message)
}

func (c *Config) applyASRDefaults(platform string) {
	if c.ASR.Provider != "" {
		return
	}
	switch c.ASR.Engine {
	case asr.EngineSherpa:
		c.ASR.Provider = asr.ProviderCPU
	case asr.EngineWhisper:
		if platform == "linux" {
			c.ASR.Provider = asr.ProviderVulkan
		}
	}
}

func isLegacyConfigPath(path string, paths PlatformPaths) bool {
	clean := filepath.Clean(path)
	for _, legacy := range paths.LegacyConfigFiles {
		if clean == filepath.Clean(legacy) && clean != filepath.Clean(paths.ConfigFile) {
			return true
		}
	}
	return false
}

func (c Config) Platform() string       { return c.source.platform }
func (c Config) ActivePath() string     { return c.source.path }
func (c Config) LegacyPathActive() bool { return c.source.legacy }
func (c Config) Paths() PlatformPaths   { return c.source.paths }

func (c Config) MigrationWarnings() []string {
	return append([]string(nil), c.source.warnings...)
}

func (c Config) IsExplicit(key string) bool {
	return c.source.explicit[key]
}

func (c *Config) Expand() error {
	var err error
	paths := []*string{
		&c.Daemon.Socket,
		&c.VAD.Model,
		&c.ASR.ModelDir,
		&c.ASR.WhisperModel,
		&c.Focus.Socket,
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
	root := c.source.paths.ModelsDir
	if root == "" {
		root = DefaultModelsRoot()
	}
	return filepath.Join(root, "whisper", c.ASR.WhisperModel+".bin")
}

func (c Config) EffectiveFocusPolicy() string {
	if c.Focus.Policy != "" {
		return c.Focus.Policy
	}
	return c.Injection.FocusPolicy
}

func (c Config) Validate() error {
	return c.ValidateFor(c.defaultCapabilitySet())
}

func (c Config) ValidateFor(capabilities CapabilitySet) error {
	if strings.TrimSpace(c.Daemon.Socket) == "" {
		return fmt.Errorf("daemon.socket must not be empty")
	}
	if !supportedLogLevel(c.Daemon.LogLevel) {
		return fmt.Errorf("daemon.log_level must be debug, info, warn, or error")
	}
	if c.Audio.Backend != "auto" && !contains(capabilities.AudioBackends, c.Audio.Backend) {
		return fmt.Errorf("audio.backend %q is unavailable on %s", c.Audio.Backend, capabilities.Platform)
	}
	if c.Audio.Format != "f32le" {
		return fmt.Errorf("audio.format must equal f32le")
	}
	if c.Audio.QuantumMS <= 0 {
		return fmt.Errorf("audio.quantum_ms must be positive")
	}
	if err := c.ValidateASRFor(capabilities); err != nil {
		return err
	}
	if c.Injection.Engine != "auto" && !contains(capabilities.InjectionEngines, c.Injection.Engine) {
		return fmt.Errorf("injection.engine %q is unavailable on %s", c.Injection.Engine, capabilities.Platform)
	}
	if c.Injection.Engine == "wtype" && strings.TrimSpace(c.Injection.WtypePath) == "" {
		return fmt.Errorf("injection.wtype_path must not be empty")
	}
	if c.Injection.Method != "" && c.Injection.Method != "unicode" {
		return fmt.Errorf("injection.method must equal unicode")
	}
	if c.Focus.Backend == "none" && c.Focus.Enabled {
		return fmt.Errorf("focus.backend=none requires focus.enabled=false")
	}
	if c.Focus.Backend != "auto" && c.Focus.Backend != "none" && !contains(capabilities.FocusBackends, c.Focus.Backend) {
		return fmt.Errorf("focus.backend %q is unavailable on %s", c.Focus.Backend, capabilities.Platform)
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
	policy := c.EffectiveFocusPolicy()
	if policy != "cancel_on_focus_change" && policy != "warn_and_type" && policy != "type_current" {
		return fmt.Errorf("unsupported focus.policy %q", policy)
	}
	if c.Hotkey.Enabled && !capabilities.HotkeyAllowed {
		return fmt.Errorf("hotkey.enabled is unsupported on %s", capabilities.Platform)
	}
	if _, err := hotkey.ResolveBinding(c.Hotkey.Key, c.Hotkey.KeyCode, c.Hotkey.Modifiers, hotkey.Mode(c.Hotkey.Mode)); err != nil {
		return err
	}
	if c.Debug.SaveAudioSegments && strings.TrimSpace(c.Debug.SaveAudioDir) == "" {
		return fmt.Errorf("debug.save_audio_dir must not be empty when debug.save_audio_segments is true")
	}
	return nil
}

func (c Config) ValidateASR() error {
	return c.ValidateASRFor(c.defaultCapabilitySet())
}

func (c Config) defaultCapabilitySet() CapabilitySet {
	if c.source.platform != "" {
		return CapabilitySetFor(c.source.platform)
	}
	return CurrentCapabilitySet()
}

func (c Config) ValidateASRFor(capabilities CapabilitySet) error {
	switch c.ASR.Engine {
	case asr.EngineSherpa:
		return c.validateSherpaASR(capabilities)
	case asr.EngineWhisper:
		provider := c.ASR.Provider
		if provider == "" {
			provider = preferredProvider(capabilities.WhisperProviders)
		}
		if !contains(capabilities.WhisperProviders, provider) {
			return fmt.Errorf("asr.provider %q is unavailable for whisper-cpp on %s", provider, capabilities.Platform)
		}
		if err := validateWhisperModelName(c.ASR.WhisperModel); err != nil {
			return err
		}
		if c.ASR.GPUDevice < 0 {
			return fmt.Errorf("asr.gpu_device must not be negative")
		}
		return nil
	case asr.EngineAuto:
		if c.ASR.Provider != "" && !contains(capabilities.WhisperProviders, c.ASR.Provider) {
			return fmt.Errorf("asr.provider %q is unavailable for auto on %s", c.ASR.Provider, capabilities.Platform)
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

func (c Config) validateSherpaASR(capabilities CapabilitySet) error {
	provider := c.ASR.Provider
	if provider == "" {
		provider = preferredProvider(capabilities.SherpaProviders)
	}
	if !contains(capabilities.SherpaProviders, provider) {
		return fmt.Errorf("asr.provider %q is unavailable for sherpa-onnx on %s", provider, capabilities.Platform)
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

func preferredProvider(providers []string) string {
	if len(providers) == 0 {
		return ""
	}
	return providers[0]
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
