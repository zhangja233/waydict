package apperr

import "errors"

const (
	CodePermissionMicrophoneDenied      = "permission_microphone_denied"
	CodePermissionAccessibilityDenied   = "permission_accessibility_denied"
	CodePermissionInputMonitoringDenied = "permission_input_monitoring_denied"
	CodeAudioBackendUnavailable         = "audio_backend_unavailable"
	CodeAudioDeviceNotFound             = "audio_device_not_found"
	CodeAudioDeviceDisconnected         = "audio_device_disconnected"
	CodeAudioStartFailed                = "audio_start_failed"
	CodeFocusUnavailable                = "focus_unavailable"
	CodeFocusChanged                    = "focus_changed"
	CodeSecureField                     = "secure_field"
	CodeInjectorUnavailable             = "injector_unavailable"
	CodeInjectionFailed                 = "injection_failed"
	CodeHotkeyUnavailable               = "hotkey_unavailable"
	CodeHotkeyQueueOverflow             = "hotkey_queue_overflow"
	CodeControlledByConfig              = "controlled_by_config"
	CodeASRBackendUnavailable           = "asr_backend_unavailable"
	CodeASRModelMissing                 = "asr_model_missing"
	CodeASRModelInvalid                 = "asr_model_invalid"
	CodeRecognitionFailed               = "recognition_failed"
	CodeConfigInvalid                   = "config_invalid"
	CodeAppNotInstalled                 = "app_not_installed"
	CodeAppTranslocated                 = "app_translocated"
	CodeModelInstallBusy                = "model_install_busy"
	CodeInternalError                   = "internal_error"
)

const (
	PermissionMicrophoneDenied      = CodePermissionMicrophoneDenied
	PermissionAccessibilityDenied   = CodePermissionAccessibilityDenied
	PermissionInputMonitoringDenied = CodePermissionInputMonitoringDenied
	AudioBackendUnavailable         = CodeAudioBackendUnavailable
	AudioDeviceNotFound             = CodeAudioDeviceNotFound
	AudioDeviceDisconnected         = CodeAudioDeviceDisconnected
	AudioStartFailed                = CodeAudioStartFailed
	FocusUnavailable                = CodeFocusUnavailable
	FocusChanged                    = CodeFocusChanged
	SecureField                     = CodeSecureField
	InjectorUnavailable             = CodeInjectorUnavailable
	InjectionFailed                 = CodeInjectionFailed
	HotkeyUnavailable               = CodeHotkeyUnavailable
	HotkeyQueueOverflow             = CodeHotkeyQueueOverflow
	ControlledByConfig              = CodeControlledByConfig
	ASRBackendUnavailable           = CodeASRBackendUnavailable
	ASRModelMissing                 = CodeASRModelMissing
	ASRModelInvalid                 = CodeASRModelInvalid
	RecognitionFailed               = CodeRecognitionFailed
	ConfigInvalid                   = CodeConfigInvalid
	AppNotInstalled                 = CodeAppNotInstalled
	AppTranslocated                 = CodeAppTranslocated
	ModelInstallBusy                = CodeModelInstallBusy
	InternalError                   = CodeInternalError
)

type Error struct {
	Code      string
	Operation string
	Retryable bool
	Hint      string
	Err       error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Operation == "" {
		if e.Err != nil {
			return e.Err.Error()
		}
		return e.Code
	}
	if e.Err == nil {
		return e.Operation
	}
	return e.Operation + ": " + e.Err.Error()
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func New(code, operation string, err error) *Error {
	return &Error{Code: code, Operation: operation, Err: err}
}

func Code(err error) string {
	if err == nil {
		return ""
	}
	var appErr *Error
	if errors.As(err, &appErr) && appErr.Code != "" {
		return appErr.Code
	}
	return CodeInternalError
}
