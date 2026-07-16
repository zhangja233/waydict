package diagnostics

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"waydict/pkg/api"
)

type Snapshot struct {
	Version            string
	Commit             string
	BuildNumber        string
	BuildTags          string
	Architecture       string
	Platform           string
	OSVersion          string
	BundlePath         string
	Translocated       bool
	VolumeReadOnly     bool
	ConfigPath         string
	LegacyConfig       bool
	MigrationWarnings  []string
	RuntimeState       string
	CompiledFeatures   map[string]bool
	ResolvedBackends   map[string]string
	Permissions        api.PermissionStatus
	Audio              AudioSnapshot
	ASR                ASRSnapshot
	VAD                VADSnapshot
	Socket             SocketSnapshot
	ModelStatus        string
	LastError          *api.ErrorInfo
	LastWarning        *api.ErrorInfo
	SigningStatus      string
	NotarizationStatus string
	QuarantineStatus   string
	NetworkAllowlist   []string
	RecentLogLines     []string
}

type AudioSnapshot struct {
	Backend           string
	SelectedDeviceID  string
	SelectedDevice    string
	DefaultDeviceID   string
	DefaultDevice     string
	SampleRate        int
	Capturing         bool
	Overruns          uint64
	InputLatencyMS    float64
	EnumerationStatus string
}

type ASRSnapshot struct {
	ConfiguredEngine   string
	ResolvedEngine     string
	ConfiguredProvider string
	ResolvedProvider   string
	Model              string
	GPUName            string
	Loaded             bool
	Check              string
}

type VADSnapshot struct {
	Engine string
	Model  string
	Check  string
}

type SocketSnapshot struct {
	Path           string
	OwnerUID       int
	Mode           string
	ConnectionTest string
}

type Output struct {
	Text string
	Data map[string]any
}

var (
	secretQueryPattern = regexp.MustCompile(`(?i)([?&](?:access_token|auth|token|api_key|secret)=)[^&\s]*`)
	bearerPattern      = regexp.MustCompile(`(?i)\bBearer\s+[^\s]+`)
	transcriptPattern  = regexp.MustCompile(`(?i)\b(?:transcript|last_transcript|last_uninjected_text|text)=(?:"(?:\\.|[^"])*"|[^\s]+)`)
)

func Build(snapshot Snapshot, home string) Output {
	sanitize := func(value string) string { return Redact(value, home) }
	features := sortedBoolPairs(snapshot.CompiledFeatures)
	backends := sortedStringPairs(snapshot.ResolvedBackends)
	warnings := make([]string, 0, len(snapshot.MigrationWarnings))
	for _, warning := range snapshot.MigrationWarnings {
		warnings = append(warnings, sanitize(warning))
	}
	logs := make([]string, 0, len(snapshot.RecentLogLines))
	for _, line := range snapshot.RecentLogLines {
		logs = append(logs, sanitize(line))
	}
	errorCode, errorMessage := errorValues(snapshot.LastError, sanitize)
	warningCode, warningMessage := errorValues(snapshot.LastWarning, sanitize)

	lines := []string{
		"Waydict Diagnostics",
		"",
		fmt.Sprintf("Waydict version: %s", snapshot.Version),
		fmt.Sprintf("Commit: %s", snapshot.Commit),
		fmt.Sprintf("Build number: %s", snapshot.BuildNumber),
		fmt.Sprintf("Build tags: %s", snapshot.BuildTags),
		fmt.Sprintf("Platform: %s", snapshot.Platform),
		fmt.Sprintf("Architecture: %s", snapshot.Architecture),
		fmt.Sprintf("OS version: %s", snapshot.OSVersion),
		fmt.Sprintf("Bundle path: %s", sanitize(snapshot.BundlePath)),
		fmt.Sprintf("App translocation detected: %t", snapshot.Translocated),
		fmt.Sprintf("Volume read-only: %t", snapshot.VolumeReadOnly),
		"",
		fmt.Sprintf("Config path: %s", sanitize(snapshot.ConfigPath)),
		fmt.Sprintf("Legacy config: %t", snapshot.LegacyConfig),
		fmt.Sprintf("Migration warnings: %s", strings.Join(warnings, "; ")),
		fmt.Sprintf("Runtime state: %s", snapshot.RuntimeState),
		fmt.Sprintf("Compiled features: %s", strings.Join(features, ", ")),
		fmt.Sprintf("Resolved backends: %s", strings.Join(backends, ", ")),
		"",
		fmt.Sprintf("Microphone permission: %s", snapshot.Permissions.Microphone),
		fmt.Sprintf("Accessibility permission: %s", snapshot.Permissions.Accessibility),
		fmt.Sprintf("Input Monitoring permission: %s", snapshot.Permissions.InputMonitoring),
		"",
		fmt.Sprintf("Audio backend: %s", snapshot.Audio.Backend),
		fmt.Sprintf("Selected audio device: %s [%s]", snapshot.Audio.SelectedDevice, snapshot.Audio.SelectedDeviceID),
		fmt.Sprintf("Default audio device: %s [%s]", snapshot.Audio.DefaultDevice, snapshot.Audio.DefaultDeviceID),
		fmt.Sprintf("Audio source: sample_rate=%d capturing=%t overruns=%d latency_ms=%.3f", snapshot.Audio.SampleRate, snapshot.Audio.Capturing, snapshot.Audio.Overruns, snapshot.Audio.InputLatencyMS),
		fmt.Sprintf("Audio enumeration: %s", sanitize(snapshot.Audio.EnumerationStatus)),
		"",
		fmt.Sprintf("ASR configured: engine=%s provider=%s", snapshot.ASR.ConfiguredEngine, snapshot.ASR.ConfiguredProvider),
		fmt.Sprintf("ASR resolved: engine=%s provider=%s model=%s gpu=%s loaded=%t", snapshot.ASR.ResolvedEngine, snapshot.ASR.ResolvedProvider, snapshot.ASR.Model, snapshot.ASR.GPUName, snapshot.ASR.Loaded),
		fmt.Sprintf("ASR model check: %s", sanitize(snapshot.ASR.Check)),
		fmt.Sprintf("VAD: engine=%s model=%s check=%s", snapshot.VAD.Engine, snapshot.VAD.Model, sanitize(snapshot.VAD.Check)),
		fmt.Sprintf("Model status: %s", snapshot.ModelStatus),
		"",
		fmt.Sprintf("Socket: %s owner_uid=%d mode=%s connection=%s", sanitize(snapshot.Socket.Path), snapshot.Socket.OwnerUID, snapshot.Socket.Mode, sanitize(snapshot.Socket.ConnectionTest)),
		fmt.Sprintf("Signing: %s", sanitize(snapshot.SigningStatus)),
		fmt.Sprintf("Notarization: %s", sanitize(snapshot.NotarizationStatus)),
		fmt.Sprintf("Quarantine: %s", sanitize(snapshot.QuarantineStatus)),
		fmt.Sprintf("Network allowlist: %s", strings.Join(snapshot.NetworkAllowlist, ", ")),
		fmt.Sprintf("Last error: %s %s", errorCode, errorMessage),
		fmt.Sprintf("Last warning: %s %s", warningCode, warningMessage),
		"",
		"Recent redacted log lines:",
	}
	if len(logs) == 0 {
		lines = append(lines, "(none)")
	} else {
		lines = append(lines, logs...)
	}
	text := strings.Join(lines, "\n")
	data := map[string]any{
		"version": snapshot.Version, "commit": snapshot.Commit, "build_number": snapshot.BuildNumber, "build_tags": snapshot.BuildTags,
		"platform": snapshot.Platform, "architecture": snapshot.Architecture, "os_version": snapshot.OSVersion,
		"bundle_path": sanitize(snapshot.BundlePath), "translocated": snapshot.Translocated, "volume_read_only": snapshot.VolumeReadOnly,
		"config_path": sanitize(snapshot.ConfigPath), "legacy_config": snapshot.LegacyConfig, "migration_warnings": warnings,
		"runtime_state": snapshot.RuntimeState, "compiled_features": snapshot.CompiledFeatures, "resolved_backends": snapshot.ResolvedBackends,
		"permissions":  map[string]any{"microphone": snapshot.Permissions.Microphone, "accessibility": snapshot.Permissions.Accessibility, "input_monitoring": snapshot.Permissions.InputMonitoring},
		"audio":        map[string]any{"backend": snapshot.Audio.Backend, "selected_device_id": snapshot.Audio.SelectedDeviceID, "selected_device_name": snapshot.Audio.SelectedDevice, "default_device_id": snapshot.Audio.DefaultDeviceID, "default_device_name": snapshot.Audio.DefaultDevice, "sample_rate": snapshot.Audio.SampleRate, "capturing": snapshot.Audio.Capturing, "overruns": snapshot.Audio.Overruns, "input_latency_ms": snapshot.Audio.InputLatencyMS, "enumeration": sanitize(snapshot.Audio.EnumerationStatus)},
		"asr":          map[string]any{"configured_engine": snapshot.ASR.ConfiguredEngine, "resolved_engine": snapshot.ASR.ResolvedEngine, "configured_provider": snapshot.ASR.ConfiguredProvider, "resolved_provider": snapshot.ASR.ResolvedProvider, "model": snapshot.ASR.Model, "gpu_name": snapshot.ASR.GPUName, "loaded": snapshot.ASR.Loaded, "check": sanitize(snapshot.ASR.Check)},
		"vad":          map[string]any{"engine": snapshot.VAD.Engine, "model": snapshot.VAD.Model, "check": sanitize(snapshot.VAD.Check)},
		"socket":       map[string]any{"path": sanitize(snapshot.Socket.Path), "owner_uid": snapshot.Socket.OwnerUID, "mode": snapshot.Socket.Mode, "connection_test": sanitize(snapshot.Socket.ConnectionTest)},
		"model_status": snapshot.ModelStatus, "last_error": map[string]string{"code": errorCode, "message": errorMessage}, "last_warning": map[string]string{"code": warningCode, "message": warningMessage},
		"signing_status": sanitize(snapshot.SigningStatus), "notarization_status": sanitize(snapshot.NotarizationStatus), "quarantine_status": sanitize(snapshot.QuarantineStatus),
		"network_allowlist": append([]string(nil), snapshot.NetworkAllowlist...), "recent_log_lines": logs, "report": text,
	}
	return Output{Text: text, Data: data}
}

func Redact(value, home string) string {
	if home != "" {
		value = strings.ReplaceAll(value, home, "~")
	}
	value = secretQueryPattern.ReplaceAllString(value, "${1}<redacted>")
	value = bearerPattern.ReplaceAllString(value, "Bearer <redacted>")
	value = transcriptPattern.ReplaceAllString(value, "transcript=<redacted>")
	return value
}

func errorValues(info *api.ErrorInfo, sanitize func(string) string) (string, string) {
	if info == nil {
		return "", ""
	}
	return info.Code, sanitize(info.Message)
}

func sortedBoolPairs(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, key := range keys {
		pairs = append(pairs, fmt.Sprintf("%s=%t", key, values[key]))
	}
	return pairs
}

func sortedStringPairs(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, key := range keys {
		pairs = append(pairs, key+"="+values[key])
	}
	return pairs
}
