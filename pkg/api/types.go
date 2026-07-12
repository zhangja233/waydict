package api

type State string

const (
	StateIdle        State = "idle"
	StateArming      State = "arming"
	StateListening   State = "listening"
	StateSegmentOpen State = "segment_open"
	StateRecognizing State = "recognizing"
	StateTyping      State = "typing"
	StateError       State = "error"
)

type Mode string

const (
	ModeToggle  Mode = "toggle"
	ModeOneshot Mode = "oneshot"
	ModeHold    Mode = "hold"
)

type ErrorInfo struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Status struct {
	State                  State           `json:"state"`
	Mode                   *Mode           `json:"mode"`
	Audio                  AudioStatus     `json:"audio"`
	VAD                    VADStatus       `json:"vad"`
	ASR                    ASRStatus       `json:"asr"`
	Injection              InjectionStatus `json:"injection"`
	Focus                  FocusStatus     `json:"focus"`
	LastError              *ErrorInfo      `json:"last_error"`
	LastWarning            *ErrorInfo      `json:"last_warning,omitempty"`
	LastTranscriptRedacted bool            `json:"last_transcript_redacted"`
	LastUninjectedText     string          `json:"last_uninjected_text,omitempty"`
	LastTranscript         string          `json:"last_transcript,omitempty"`
	UptimeSeconds          float64         `json:"uptime_seconds,omitempty"`
}

type AudioStatus struct {
	Backend    string  `json:"backend"`
	SampleRate int     `json:"sample_rate"`
	LevelDBFS  float64 `json:"level_dbfs"`
	Overruns   uint64  `json:"overruns"`
	Capturing  bool    `json:"capturing"`
}

// VADStatus reports the voice-activity-detection engine actually in use. It can
// differ from the configured engine: if vad.engine="silero" but the silero model
// is missing, the daemon falls back to "energy" — compare with config to detect that.
type VADStatus struct {
	Engine string `json:"engine"`
}

type ASRStatus struct {
	Engine         string `json:"engine"`
	Model          string `json:"model"`
	Provider       string `json:"provider"`
	ResolvedEngine string `json:"resolved_engine"`
	// ResolvedProvider is the requested provider until Loaded is true; after
	// load it reflects the backend the native library actually selected, and
	// GPUName is only set once a GPU backend is confirmed.
	ResolvedProvider string  `json:"resolved_provider"`
	GPUName          string  `json:"gpu_name"`
	FallbackReason   string  `json:"fallback_reason"`
	NumThreads       int     `json:"num_threads"`
	Loaded           bool    `json:"loaded"`
	LastRTF          float64 `json:"last_rtf"`
}

type InjectionStatus struct {
	Engine    string `json:"engine"`
	Available bool   `json:"available"`
	LastError string `json:"last_error,omitempty"`
}

type FocusStatus struct {
	Sway        bool   `json:"sway"`
	FocusedID   int64  `json:"focused_id,omitempty"`
	FocusedName string `json:"focused_name,omitempty"`
	AppID       string `json:"app_id,omitempty"`
	Class       string `json:"class,omitempty"`
	Workspace   string `json:"workspace,omitempty"`
	Output      string `json:"output,omitempty"`
}
