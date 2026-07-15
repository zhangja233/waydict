//go:build !darwin || !cgo

package appkit

import (
	"context"
	"errors"
)

const MaxActionPayloadBytes = 4 * 1024

type Action int32

const (
	ActionStartHold Action = iota + 1
	ActionToggle
	ActionStartOneshot
	ActionStopCommit
	ActionStopDiscard
	ActionReloadConfig
	ActionInstallRequiredModels
	ActionRevealModels
	ActionSelectAudioDevice
	ActionSetHotkeyMode
	ActionRequestMicrophonePermission
	ActionRequestAccessibilityPermission
	ActionRequestInputMonitoringPermission
	ActionSetLaunchAtLogin
	ActionOpenConfig
	ActionRestartRuntime
	ActionOpenLog
	ActionRunDiagnostics
	ActionCopyDiagnostics
	ActionQuit
)

type Event struct {
	Action  Action
	Payload string
	Number  int64
}

type Installation struct {
	BundlePath   string `json:"bundle_path"`
	Translocated bool   `json:"translocated"`
	ReadOnly     bool   `json:"read_only"`
	Blocked      bool   `json:"blocked"`
}

type ViewModel struct {
	State                string
	LastError            string
	LastWarning          string
	MicrophonePermission string
	Accessibility        string
	InputMonitoring      string
	LaunchAtLogin        bool
	LaunchAtLoginError   string
	HotkeyMode           string
	HotkeyDescription    string
	HotkeyAvailable      bool
	AudioDeviceName      string
	ASREngine            string
	ASRModel             string
	ASRProvider          string
	ModelsReady          bool
	ModelStatus          string
	PendingRestart       bool
	InstallingModels     bool
	InstallationBlocked  bool
	InstallationMessage  string
	Version              string
	Commit               string
	BuildNumber          string
	BuildTags            string
	Architecture         string
	Platform             string
	ConfigPath           string
	LegacyConfig         bool
	MigrationWarning     string
	AudioBackend         string
	InjectionBackend     string
	FocusBackend         string
	SocketPath           string
}

type Host struct{}

var errUnavailable = errors.New("AppKit host requires Darwin with cgo")

func New() (*Host, error)                              { return nil, errUnavailable }
func (*Host) Installation() Installation               { return Installation{} }
func (*Host) Events() <-chan Event                     { return nil }
func (*Host) Run() error                               { return errUnavailable }
func (*Host) Terminate()                               {}
func (*Host) Destroy()                                 {}
func (*Host) Update(ViewModel) error                   { return errUnavailable }
func (*Host) ShowError(string, string)                 {}
func (*Host) ShowDiagnostics(bool)                     {}
func (*Host) OpenPath(context.Context, string) error   { return errUnavailable }
func (*Host) RevealPath(context.Context, string) error { return errUnavailable }
