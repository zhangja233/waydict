package app

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"waydict/internal/apperr"
	"waydict/internal/audio"
	"waydict/internal/config"
	"waydict/internal/control"
	"waydict/internal/focus"
	"waydict/internal/hotkey"
	"waydict/internal/inject"
	"waydict/internal/permissions"
	"waydict/internal/preferences"
	"waydict/pkg/api"
)

func (a *App) handleExtendedControl(ctx context.Context, req control.Request) (control.Response, bool) {
	fail := func(err error) (control.Response, bool) {
		return control.Fail(req.ID, codeFor(err), err.Error(), a.Status(ctx)), true
	}
	switch req.Command {
	case "capabilities":
		return control.OKData(req.ID, a.Status(ctx), a.capabilityData()), true
	case "permissions":
		data, err := a.permissionData(ctx)
		if err != nil {
			return fail(err)
		}
		return control.OKData(req.ID, a.Status(ctx), data), true
	case "request_microphone_permission":
		return a.requestPermission(ctx, req.ID, permissions.KindMicrophone), true
	case "request_accessibility_permission":
		return a.requestPermission(ctx, req.ID, permissions.KindAccessibility), true
	case "request_input_monitoring_permission":
		return a.requestPermission(ctx, req.ID, permissions.KindInputMonitoring), true
	case "inject_text":
		text, ok := req.Args["text"].(string)
		if !ok {
			return fail(withCode("usage", fmt.Errorf("inject_text requires string text")))
		}
		peerUID, _ := control.PeerUID(ctx)
		a.logInfo("inject text request", "text_bytes", len([]byte(text)), "request_id", req.ID, "peer_uid", peerUID, "result", "started")
		a.mu.Lock()
		defaultTimeoutMS := a.cfg.Injection.TimeoutMS
		a.mu.Unlock()
		timeout, err := optionalTimeout(req.Args, defaultTimeoutMS)
		if err != nil {
			a.logInfo("inject text request", "text_bytes", len([]byte(text)), "request_id", req.ID, "peer_uid", peerUID, "result", "rejected")
			return fail(withCode("usage", err))
		}
		if err := a.InjectFinalText(ctx, text, timeout); err != nil {
			a.logInfo("inject text request", "text_bytes", len([]byte(text)), "request_id", req.ID, "peer_uid", peerUID, "result", apperr.Code(err))
			return fail(err)
		}
		a.logInfo("inject text request", "text_bytes", len([]byte(text)), "request_id", req.ID, "peer_uid", peerUID, "result", "ok")
		return control.OK(req.ID, a.Status(ctx)), true
	case "list_audio_devices":
		devices, err := a.audioDevices(ctx)
		if err != nil {
			return fail(err)
		}
		return control.OKData(req.ID, a.Status(ctx), map[string]any{"devices": devices}), true
	case "set_audio_device":
		id, ok := req.Args["id"].(string)
		if !ok {
			return fail(withCode("usage", fmt.Errorf("set_audio_device requires string id")))
		}
		if err := a.SetAudioDevice(ctx, id); err != nil {
			return fail(err)
		}
		return control.OK(req.ID, a.Status(ctx)), true
	case "set_hotkey_mode":
		mode, ok := req.Args["mode"].(string)
		if !ok {
			return fail(withCode("usage", fmt.Errorf("set_hotkey_mode requires string mode")))
		}
		if err := a.SetHotkeyMode(ctx, mode); err != nil {
			return fail(err)
		}
		return control.OK(req.ID, a.Status(ctx)), true
	case "set_launch_at_login":
		enabled, ok := req.Args["enabled"].(bool)
		if !ok {
			return fail(withCode("usage", fmt.Errorf("set_launch_at_login requires boolean enabled")))
		}
		if a.loginItem == nil {
			return fail(withCode("dependency_missing", fmt.Errorf("launch-at-login service is unavailable")))
		}
		if err := a.loginItem.SetEnabled(ctx, enabled); err != nil {
			return fail(err)
		}
		enabled, err := a.loginItem.Status(ctx)
		if err != nil {
			return fail(err)
		}
		return control.OKData(req.ID, a.Status(ctx), map[string]any{"enabled": enabled}), true
	case "restart_runtime":
		if err := a.RestartRuntime(ctx); err != nil {
			return fail(err)
		}
		return control.OK(req.ID, a.Status(ctx)), true
	case "activate_app":
		if a.hostActions.Activate == nil {
			return fail(withCode("dependency_missing", fmt.Errorf("app activation is unavailable")))
		}
		if err := a.hostActions.Activate(ctx); err != nil {
			return fail(err)
		}
		return control.OK(req.ID, a.Status(ctx)), true
	case "install_required_models":
		return a.runHostAction(ctx, req.ID, a.hostActions.InstallRequiredModels, nil), true
	case "reveal_models":
		return a.runHostAction(ctx, req.ID, a.hostActions.RevealModels, nil), true
	case "open_config":
		return a.runHostAction(ctx, req.ID, a.hostActions.OpenConfig, nil), true
	case "open_log":
		return a.runHostAction(ctx, req.ID, a.hostActions.OpenLog, nil), true
	case "run_diagnostics":
		if a.hostActions.RunDiagnostics == nil {
			return fail(withCode("dependency_missing", fmt.Errorf("diagnostics runner is unavailable")))
		}
		data, err := a.hostActions.RunDiagnostics(ctx)
		if err != nil {
			return fail(err)
		}
		return control.OKData(req.ID, a.Status(ctx), data), true
	case "copy_diagnostics":
		return a.runHostAction(ctx, req.ID, a.hostActions.CopyDiagnostics, nil), true
	default:
		return control.Response{}, false
	}
}

func (a *App) capabilityData() map[string]any {
	caps := a.capabilities
	return map[string]any{
		"platform":          caps.Platform,
		"host":              caps.Host,
		"audio_backends":    append([]string(nil), caps.AudioBackends...),
		"injection_engines": append([]string(nil), caps.InjectionEngines...),
		"focus_backends":    append([]string(nil), caps.FocusBackends...),
		"whisper_providers": append([]string(nil), caps.WhisperProviders...),
		"sherpa_providers":  append([]string(nil), caps.SherpaProviders...),
		"hotkey_available":  caps.HotkeyAvailable,
		"app_host":          caps.Host == "macos_app",
	}
}

func (a *App) permissionData(ctx context.Context) (map[string]any, error) {
	if a.permissionSource == nil {
		return nil, withCode("dependency_missing", fmt.Errorf("permission service is unavailable"))
	}
	snapshot, err := a.permissionSource.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"microphone":       snapshot.Microphone,
		"accessibility":    snapshot.Accessibility,
		"input_monitoring": snapshot.InputMonitoring,
		"checked_at":       snapshot.CheckedAt,
	}, nil
}

func (a *App) requestPermission(ctx context.Context, id string, kind permissions.Kind) control.Response {
	if a.permissionSource == nil {
		err := withCode("dependency_missing", fmt.Errorf("permission service is unavailable"))
		return control.Fail(id, codeFor(err), err.Error(), a.Status(ctx))
	}
	state, err := a.permissionSource.Request(ctx, kind)
	if err != nil {
		return control.Fail(id, codeFor(err), err.Error(), a.Status(ctx))
	}
	if kind == permissions.KindMicrophone && (state == permissions.Denied || state == permissions.Restricted) {
		err := apperr.New(apperr.CodePermissionMicrophoneDenied, "request microphone permission", fmt.Errorf("microphone permission is %s", state))
		return control.Fail(id, codeFor(err), err.Error(), a.Status(ctx))
	}
	return control.OKData(id, a.Status(ctx), map[string]any{"kind": kind, "state": state})
}

func (a *App) InjectFinalText(ctx context.Context, text string, timeout time.Duration) error {
	if !utf8.ValidString(text) {
		return withCode("usage", fmt.Errorf("text is not valid UTF-8"))
	}
	if strings.IndexByte(text, 0) >= 0 {
		return withCode("usage", fmt.Errorf("text contains NUL"))
	}
	if len([]byte(text)) > control.MaxInjectTextBytes {
		return withCode("usage", fmt.Errorf("text exceeds %d bytes", control.MaxInjectTextBytes))
	}
	a.mu.Lock()
	cfg := a.cfg
	injector := a.injector
	provider := a.focus
	a.mu.Unlock()
	if injector == nil {
		return apperr.New(apperr.CodeInjectorUnavailable, "inject final text", fmt.Errorf("injector is unavailable"))
	}
	var (
		target focus.Target
		guard  *focus.Guard
	)
	if cfg.Focus.Enabled {
		guard = focus.NewGuard(provider, focus.Policy(cfg.EffectiveFocusPolicy()))
		if err := guard.CaptureStart(ctx, 0); err != nil {
			return err
		}
		defer guard.Reset()
		var err error
		target, _, err = guard.ResolveForInjection(ctx)
		if err != nil {
			return err
		}
		defer provider.Release(target)
	}
	if err := injector.Available(ctx); err != nil {
		return normalizeError(apperr.CodeInjectorUnavailable, "check injector", err)
	}
	request := inject.Request{
		Text:     text,
		Target:   inject.Target{Focus: target},
		KeyDelay: time.Duration(cfg.Injection.KeyDelayMS) * time.Millisecond,
	}
	if timeout > 0 {
		request.Deadline = time.Now().Add(timeout)
	}
	if target.Token != 0 || target.DegradedReason != "" {
		request.ValidateTarget = func(ctx context.Context, target focus.Target) error {
			return focus.ValidateTarget(ctx, provider, target)
		}
	}
	if err := injector.Inject(ctx, request); err != nil {
		return normalizeError(apperr.CodeInjectionFailed, "inject final text", err)
	}
	return nil
}

func optionalTimeout(args map[string]any, fallbackMS int) (time.Duration, error) {
	value, ok := args["timeout_ms"]
	if !ok {
		return time.Duration(fallbackMS) * time.Millisecond, nil
	}
	var milliseconds int64
	switch value := value.(type) {
	case float64:
		milliseconds = int64(value)
		if float64(milliseconds) != value {
			return 0, fmt.Errorf("timeout_ms must be an integer")
		}
	case int:
		milliseconds = int64(value)
	case int64:
		milliseconds = value
	default:
		return 0, fmt.Errorf("timeout_ms must be an integer")
	}
	if milliseconds <= 0 || milliseconds > 300000 {
		return 0, fmt.Errorf("timeout_ms must be between 1 and 300000")
	}
	return time.Duration(milliseconds) * time.Millisecond, nil
}

func (a *App) audioDevices(ctx context.Context) ([]audio.Device, error) {
	if a.deviceManager == nil {
		return nil, withCode("dependency_missing", fmt.Errorf("audio device manager is unavailable"))
	}
	return a.deviceManager.Devices(ctx)
}

func (a *App) SetAudioDevice(ctx context.Context, id string) error {
	if err := a.requireIdle(); err != nil {
		return err
	}
	a.mu.Lock()
	controlled := a.cfg.IsExplicit("audio.device")
	a.mu.Unlock()
	if controlled {
		return apperr.New(apperr.CodeControlledByConfig, "set audio device", fmt.Errorf("audio.device is controlled by config.toml"))
	}
	if id == "default" {
		id = ""
	}
	if id != "" {
		devices, err := a.audioDevices(ctx)
		if err != nil {
			return err
		}
		found := false
		for _, device := range devices {
			if device.ID == id && device.Connected {
				found = true
				break
			}
		}
		if !found {
			return apperr.New(apperr.CodeAudioDeviceNotFound, "set audio device", fmt.Errorf("audio device %q was not found", id))
		}
	}
	if a.preferences == nil {
		return withCode("dependency_missing", fmt.Errorf("preference store is unavailable"))
	}
	if id == "" {
		if err := a.preferences.Delete(ctx, preferences.KeySelectedAudioDeviceUID); err != nil {
			return err
		}
	} else if err := a.preferences.SetString(ctx, preferences.KeySelectedAudioDeviceUID, id); err != nil {
		return err
	}
	a.mu.Lock()
	a.cfg.Audio.Device = id
	a.mu.Unlock()
	return a.recreateSource(ctx)
}

func (a *App) SetHotkeyMode(ctx context.Context, mode string) error {
	if mode != "hold" && mode != "toggle" && mode != "oneshot" {
		return withCode("usage", fmt.Errorf("mode must be hold, toggle, or oneshot"))
	}
	a.mu.Lock()
	controlled := a.cfg.IsExplicit("hotkey.mode")
	a.mu.Unlock()
	if controlled {
		return apperr.New(apperr.CodeControlledByConfig, "set hotkey mode", fmt.Errorf("hotkey.mode is controlled by config.toml"))
	}
	if a.preferences == nil {
		return withCode("dependency_missing", fmt.Errorf("preference store is unavailable"))
	}
	if err := a.preferences.SetString(ctx, preferences.KeySelectedHotkeyMode, mode); err != nil {
		return err
	}
	a.mu.Lock()
	a.cfg.Hotkey.Mode = mode
	if a.status.Hotkey != nil {
		a.status.Hotkey.Mode = api.Mode(mode)
	}
	cfg := a.cfg.Hotkey
	a.mu.Unlock()
	if a.hotkey != nil {
		binding, err := hotkeyBinding(cfg)
		if err != nil {
			return err
		}
		if err := a.hotkey.Rebind(ctx, binding); err != nil {
			return err
		}
	}
	return nil
}

func hotkeyBinding(cfg config.Hotkey) (hotkey.Binding, error) {
	modifiers, err := hotkey.ParseModifiers(cfg.Modifiers)
	if err != nil {
		return hotkey.Binding{}, err
	}
	return hotkey.Binding{
		Key:       cfg.Key,
		KeyCode:   uint16(max(cfg.KeyCode, 0)),
		Modifiers: modifiers,
		Mode:      hotkey.Mode(cfg.Mode),
	}, nil
}

func (a *App) RestartRuntime(ctx context.Context) error {
	if err := a.requireIdle(); err != nil {
		return err
	}
	a.mu.Lock()
	pending := a.pendingConfig
	a.mu.Unlock()
	if pending == nil {
		return nil
	}
	if a.hostActions.RestartRuntime == nil {
		return withCode("restart_required", fmt.Errorf("runtime restart is not wired"))
	}
	if err := a.hostActions.RestartRuntime(ctx, *pending); err != nil {
		return err
	}
	a.mu.Lock()
	a.pendingConfig = nil
	a.status.PendingRestart = false
	if a.status.LastWarning != nil && a.status.LastWarning.Code == "restart_required" {
		a.status.LastWarning = nil
	}
	a.mu.Unlock()
	return nil
}

func (a *App) requireIdle() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.capturing || a.pendingASR > 0 || len(a.deferred) > 0 || (a.status.State != api.StateIdle && a.status.State != api.StateError) {
		return withCode("busy", fmt.Errorf("operation requires an idle runtime"))
	}
	return nil
}

func (a *App) runHostAction(ctx context.Context, id string, action func(context.Context) error, data map[string]any) control.Response {
	if action == nil {
		err := withCode("dependency_missing", fmt.Errorf("host action is unavailable"))
		return control.Fail(id, codeFor(err), err.Error(), a.Status(ctx))
	}
	if err := action(ctx); err != nil {
		return control.Fail(id, codeFor(err), err.Error(), a.Status(ctx))
	}
	if data != nil {
		return control.OKData(id, a.Status(ctx), data)
	}
	return control.OK(id, a.Status(ctx))
}
