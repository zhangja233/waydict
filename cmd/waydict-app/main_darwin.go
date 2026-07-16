//go:build darwin && cgo

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"waydict/internal/app"
	"waydict/internal/apperr"
	"waydict/internal/buildinfo"
	"waydict/internal/config"
	"waydict/internal/control"
	"waydict/internal/hotkey"
	"waydict/internal/macos/appkit"
	"waydict/internal/model"
	"waydict/internal/modelinstall"
	"waydict/internal/permissions"
	"waydict/internal/platform"
	"waydict/internal/preferences"
	"waydict/pkg/api"
)

const (
	waydictBundleID = "io.github.zhangja233.waydict"
	moveMessage     = "Move Waydict to a writable Applications folder, quit this copy, and reopen it"
)

func main() {
	runtime.LockOSThread()
	host, err := appkit.New()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	defer host.Destroy()

	root, cancel := context.WithCancel(context.Background())
	controller := &appController{host: host, cancel: cancel, installation: host.Installation()}
	done := make(chan struct{})
	go func() {
		defer close(done)
		controller.run(root)
	}()

	if err := host.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		fmt.Fprintln(os.Stderr, "timed out waiting for Waydict runtime shutdown")
	}
}

type appController struct {
	host                  *appkit.Host
	cancel                context.CancelFunc
	installation          appkit.Installation
	cfg                   config.Config
	services              platform.Services
	runtime               *app.Runtime
	installing            atomic.Bool
	modelsReady           atomic.Bool
	modelStatus           atomic.Value
	hotkeyEvents          chan hotkey.Event
	hotkeyHold            uint64
	hotkeyMu              sync.Mutex
	hotkeyRestartRequired atomic.Bool
}

func (c *appController) run(ctx context.Context) {
	defer c.host.Terminate()
	if c.installation.Blocked {
		c.runWithoutRuntime(ctx, apperr.New(apperr.CodeAppTranslocated, "check application location", errors.New(moveMessage)), true)
		return
	}

	cfg, err := config.Load("")
	if err != nil {
		c.runWithoutRuntime(ctx, apperr.New(apperr.CodeConfigInvalid, "load config", err), false)
		return
	}
	c.cfg = cfg

	probeCtx, probeCancel := context.WithTimeout(ctx, 150*time.Millisecond)
	_, probeErr := control.Send(probeCtx, cfg.Daemon.Socket, control.NewRequest("status", nil))
	probeCancel()
	if probeErr == nil {
		activator := appkit.NewActivator()
		activateCtx, activateCancel := context.WithTimeout(context.Background(), time.Second)
		if err := activator.ActivateBundle(activateCtx, waydictBundleID); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		activateCancel()
		c.cancel()
		return
	}
	c.checkModels()

	c.services = platform.Current()
	dependencies := c.platformDependencies()
	rt, err := app.NewRuntime(ctx, cfg, app.RuntimeOptions{
		Platform:             dependencies,
		AllowDegradedStartup: true,
		NewWhisper:           appWhisperFactory,
		ProbeAccelerator:     appAcceleratorProbe,
		Shutdown:             c.cancel,
	})
	if err != nil {
		c.runWithoutRuntime(ctx, err, false)
		return
	}
	c.runtime = rt
	c.hotkeyEvents = make(chan hotkey.Event, 64)

	serveResults := make(chan error, 1)
	go func() {
		serveResults <- rt.Serve(ctx)
	}()
	serveStopped := false
	defer func() {
		c.cancel()
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 3*time.Second)
		if err := rt.Close(closeCtx); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		closeCancel()
		if !serveStopped {
			select {
			case <-serveResults:
			case <-time.After(2 * time.Second):
				fmt.Fprintln(os.Stderr, "timed out waiting for control socket shutdown")
			}
		}
	}()

	go c.dispatchEvents(ctx)
	if err := c.syncHotkey(ctx, false); err != nil && apperr.Code(err) != apperr.CodePermissionInputMonitoringDenied {
		c.host.ShowError(apperr.Code(err), err.Error())
	}
	go c.pollStatus(ctx)

	select {
	case <-ctx.Done():
		return
	case serveErr := <-serveResults:
		serveStopped = true
		if errors.Is(serveErr, control.ErrAlreadyRunning) {
			activateCtx, activateCancel := context.WithTimeout(context.Background(), time.Second)
			if c.services.AppActivation != nil {
				if err := c.services.AppActivation.ActivateBundle(activateCtx, waydictBundleID); err != nil {
					fmt.Fprintln(os.Stderr, err)
				}
			}
			activateCancel()
			c.cancel()
			return
		}
		if serveErr != nil {
			c.host.ShowError(apperr.CodeInternalError, serveErr.Error())
			<-ctx.Done()
		}
	}
}

func (c *appController) platformDependencies() app.PlatformDependencies {
	services := c.services
	dependencies := app.PlatformDependencies{
		Name: services.Capabilities.OS,
		Capabilities: app.ControlCapabilities{
			Platform:         services.Capabilities.OS,
			Host:             services.Capabilities.Host,
			AudioBackends:    services.Capabilities.AudioBackends,
			InjectionEngines: services.Capabilities.InjectionBackends,
			FocusBackends:    services.Capabilities.FocusBackends,
			WhisperProviders: services.Capabilities.WhisperProviders,
			SherpaProviders:  services.Capabilities.SherpaProviders,
			HotkeyAvailable:  services.Capabilities.HotkeyAvailable,
		},
		NewSource:        services.NewAudio,
		NewInjector:      services.NewInjector,
		NewFocusProvider: services.NewFocus,
		PermissionSource: services.Permissions,
		DeviceManager:    services.Devices,
		Preferences:      services.Preferences,
		Hotkey:           services.Hotkey,
		LoginItem:        services.LoginItem,
	}
	if services.AppActivation != nil {
		dependencies.HostActions.Activate = func(ctx context.Context) error {
			return services.AppActivation.ActivateBundle(ctx, waydictBundleID)
		}
	}
	dependencies.HostActions.InstallRequiredModels = c.installRequiredModels
	dependencies.HostActions.RevealModels = func(ctx context.Context) error {
		path := c.cfg.Paths().ModelsDir
		if err := os.MkdirAll(path, 0755); err != nil {
			return err
		}
		return c.host.RevealPath(ctx, path)
	}
	dependencies.HostActions.OpenConfig = c.openConfig
	dependencies.HostActions.OpenLog = c.openLog
	dependencies.HostActions.RunDiagnostics = c.diagnosticsData
	dependencies.HostActions.CopyDiagnostics = func(context.Context) error {
		c.host.ShowDiagnostics(true)
		return nil
	}
	return dependencies
}

func (c *appController) dispatchEvents(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-c.host.Events():
			if err := c.handleEvent(ctx, event); err != nil {
				c.host.ShowError(apperr.Code(err), err.Error())
			}
		case event := <-c.hotkeyEvents:
			if err := c.handleHotkeyEvent(ctx, event); err != nil {
				c.host.ShowError(apperr.Code(err), err.Error())
			}
		}
	}
}

func (c *appController) handleEvent(ctx context.Context, event appkit.Event) error {
	switch event.Action {
	case appkit.ActionStartHold:
		return c.start(ctx, api.ModeHold, event.Number)
	case appkit.ActionToggle:
		status := c.runtime.App.Status(ctx)
		if status.State != api.StateIdle && status.State != api.StateError {
			return c.runtime.App.Stop(ctx, true)
		}
		return c.start(ctx, api.ModeToggle, event.Number)
	case appkit.ActionStartOneshot:
		return c.start(ctx, api.ModeOneshot, event.Number)
	case appkit.ActionStopCommit:
		return c.runtime.App.Stop(ctx, true)
	case appkit.ActionStopDiscard:
		return c.runtime.App.Stop(ctx, false)
	case appkit.ActionReloadConfig:
		if err := c.runtime.App.ReloadConfig(ctx); err != nil {
			return err
		}
		return c.syncHotkey(ctx, true)
	case appkit.ActionInstallRequiredModels:
		go func() {
			if err := c.installRequiredModels(ctx); err != nil {
				c.host.ShowError(apperr.Code(err), err.Error())
				return
			}
			if err := c.runtime.RestartASR(ctx); err != nil {
				c.host.ShowError(apperr.Code(err), err.Error())
			}
		}()
		return nil
	case appkit.ActionRevealModels:
		return c.runtime.Platform.HostActions.RevealModels(ctx)
	case appkit.ActionSelectAudioDevice:
		if event.Payload == "" || strings.IndexByte(event.Payload, 0) >= 0 {
			return errors.New("audio device UID is empty")
		}
		return c.runtime.App.SetAudioDevice(ctx, event.Payload)
	case appkit.ActionSetHotkeyMode:
		if event.Payload != "hold" && event.Payload != "toggle" && event.Payload != "oneshot" {
			return fmt.Errorf("invalid hotkey mode %q", event.Payload)
		}
		if err := c.runtime.App.SetHotkeyMode(ctx, event.Payload); err != nil {
			return err
		}
		return c.syncHotkey(ctx, false)
	case appkit.ActionRequestMicrophonePermission:
		return c.requestPermission(ctx, permissions.KindMicrophone)
	case appkit.ActionRequestAccessibilityPermission:
		return c.requestPermission(ctx, permissions.KindAccessibility)
	case appkit.ActionRequestInputMonitoringPermission:
		return c.requestPermission(ctx, permissions.KindInputMonitoring)
	case appkit.ActionSetLaunchAtLogin:
		if event.Payload != "true" && event.Payload != "false" {
			return fmt.Errorf("invalid launch-at-login value %q", event.Payload)
		}
		enabled, err := strconv.ParseBool(event.Payload)
		if err != nil {
			return err
		}
		if c.runtime.Platform.LoginItem == nil {
			return errors.New("launch-at-login service is unavailable")
		}
		return c.runtime.Platform.LoginItem.SetEnabled(ctx, enabled)
	case appkit.ActionOpenConfig:
		return c.openConfig(ctx)
	case appkit.ActionRestartRuntime:
		if c.hotkeyRestartRequired.Load() {
			return c.syncHotkey(ctx, true)
		}
		return c.runtime.App.RestartRuntime(ctx)
	case appkit.ActionOpenLog:
		return c.openLog(ctx)
	case appkit.ActionRunDiagnostics:
		c.host.ShowDiagnostics(false)
		return nil
	case appkit.ActionCopyDiagnostics:
		c.host.ShowDiagnostics(true)
		return nil
	case appkit.ActionSystemWillSleep:
		if c.runtime.App.Status(ctx).Audio.Capturing {
			return c.runtime.App.Stop(ctx, false)
		}
		return nil
	case appkit.ActionSystemDidWake:
		return c.runtime.RecreateAudio(ctx)
	case appkit.ActionQuit:
		c.cancel()
		return nil
	default:
		return fmt.Errorf("unsupported AppKit action %d", event.Action)
	}
}

func (c *appController) start(ctx context.Context, mode api.Mode, pid int64) error {
	if pid <= 0 || pid > int64(^uint32(0)>>1) {
		return apperr.New(apperr.CodeFocusChanged, "start menu dictation", errors.New("target application is unavailable"))
	}
	targetPID := int(pid)
	origin := app.StartOriginMenu
	if targetPID == os.Getpid() {
		origin = app.StartOriginTest
	} else if err := c.activateTarget(ctx, targetPID); err != nil {
		return err
	}
	return c.runtime.App.StartWithOptions(ctx, app.StartOptions{
		Mode:             mode,
		Origin:           origin,
		ExpectedFocusPID: targetPID,
	})
}

func (c *appController) activateTarget(ctx context.Context, pid int) error {
	activator := c.services.AppActivation
	if activator == nil {
		return apperr.New(apperr.CodeFocusChanged, "activate target", errors.New("application activation is unavailable"))
	}
	probeCtx, probeCancel := context.WithTimeout(ctx, time.Millisecond)
	err := activator.WaitFrontmostPID(probeCtx, pid)
	probeCancel()
	if err == nil {
		return nil
	}
	if err := activator.ActivatePID(ctx, pid); err != nil {
		return apperr.New(apperr.CodeFocusChanged, "activate target", err)
	}
	waitCtx, waitCancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer waitCancel()
	if err := activator.WaitFrontmostPID(waitCtx, pid); err != nil {
		return apperr.New(apperr.CodeFocusChanged, "confirm target activation", err)
	}
	return nil
}

func (c *appController) handleHotkeyEvent(ctx context.Context, event hotkey.Event) error {
	service := c.runtime.Platform.Hotkey
	if service == nil {
		return apperr.New(apperr.CodeHotkeyUnavailable, "dispatch global hotkey", errors.New("hotkey service is unavailable"))
	}
	mode := service.Status().Binding.Mode
	switch event.Action {
	case hotkey.Press:
		switch mode {
		case hotkey.ModeHold:
			if _, _, active := c.runtime.App.ActiveSession(); active {
				return nil
			}
			if err := c.runtime.App.StartWithOptions(ctx, app.StartOptions{
				Mode:   api.ModeHold,
				Origin: app.StartOriginHotkey,
			}); err != nil {
				return err
			}
			if session, activeMode, active := c.runtime.App.ActiveSession(); active && activeMode == api.ModeHold {
				c.hotkeyHold = session
			}
			return nil
		case hotkey.ModeToggle:
			return c.runtime.App.ToggleWithOptions(ctx, app.StartOptions{
				Origin: app.StartOriginHotkey,
			})
		case hotkey.ModeOneshot:
			return c.runtime.App.StartWithOptions(ctx, app.StartOptions{
				Mode:   api.ModeOneshot,
				Origin: app.StartOriginHotkey,
			})
		default:
			return apperr.New(apperr.CodeHotkeyUnavailable, "dispatch global hotkey", fmt.Errorf("unsupported mode %q", mode))
		}
	case hotkey.Release:
		session := c.hotkeyHold
		c.hotkeyHold = 0
		if activeSession, activeMode, active := c.runtime.App.ActiveSession(); active && activeMode == api.ModeHold && activeSession == session && session != 0 {
			return c.runtime.App.Release(ctx)
		}
		return nil
	case hotkey.Abort:
		session := c.hotkeyHold
		c.hotkeyHold = 0
		if activeSession, activeMode, active := c.runtime.App.ActiveSession(); active && activeMode == api.ModeHold && activeSession == session && session != 0 {
			return c.runtime.App.Stop(ctx, false)
		}
		return nil
	default:
		return fmt.Errorf("unsupported hotkey action %q", event.Action)
	}
}

func (c *appController) syncHotkey(ctx context.Context, retryUnavailable bool) error {
	c.hotkeyMu.Lock()
	defer c.hotkeyMu.Unlock()
	service := c.runtime.Platform.Hotkey
	if service == nil {
		return apperr.New(apperr.CodeHotkeyUnavailable, "configure global hotkey", errors.New("hotkey service is unavailable"))
	}
	enabled, binding, err := c.runtime.App.HotkeyBinding()
	if err != nil {
		return apperr.New(apperr.CodeConfigInvalid, "resolve global hotkey", err)
	}
	status := service.Status()
	if !enabled {
		c.hotkeyRestartRequired.Store(false)
		if status.Running {
			return service.Stop(ctx)
		}
		return nil
	}
	if c.runtime.Platform.PermissionSource == nil {
		return apperr.New(apperr.CodeHotkeyUnavailable, "configure global hotkey", errors.New("permission service is unavailable"))
	}
	snapshot, err := c.runtime.Platform.PermissionSource.Snapshot(ctx)
	if err != nil {
		return err
	}
	if snapshot.InputMonitoring != permissions.Granted {
		return apperr.New(apperr.CodePermissionInputMonitoringDenied, "configure global hotkey", errors.New("Input Monitoring permission is not granted"))
	}
	if status.Running {
		if status.Binding != binding {
			if err := service.Rebind(ctx, binding); err != nil {
				return err
			}
		}
		c.hotkeyRestartRequired.Store(false)
		return nil
	}
	if !retryUnavailable && status.LastErrorCode != "" && status.LastErrorCode != apperr.CodePermissionInputMonitoringDenied {
		return apperr.New(status.LastErrorCode, "configure global hotkey", errors.New("listener is unavailable"))
	}
	if err := service.Stop(ctx); err != nil {
		return err
	}
	handler := func(event hotkey.Event) {
		select {
		case c.hotkeyEvents <- event:
		case <-ctx.Done():
		}
	}
	if err := service.Start(ctx, binding, handler); err != nil {
		c.hotkeyRestartRequired.Store(true)
		return err
	}
	c.hotkeyRestartRequired.Store(false)
	return nil
}

func (c *appController) requestPermission(ctx context.Context, kind permissions.Kind) error {
	if c.runtime.Platform.PermissionSource == nil {
		return errors.New("permission service is unavailable")
	}
	state, err := c.runtime.Platform.PermissionSource.Request(ctx, kind)
	if err != nil {
		return err
	}
	if kind == permissions.KindMicrophone && state == permissions.Denied {
		_ = c.runtime.Platform.PermissionSource.OpenSettings(ctx, kind)
		return apperr.New(apperr.CodePermissionMicrophoneDenied, "request microphone permission", errors.New("microphone permission is denied"))
	}
	if kind == permissions.KindMicrophone && state == permissions.Restricted {
		return apperr.New(apperr.CodePermissionMicrophoneDenied, "request microphone permission", errors.New("microphone permission is restricted"))
	}
	if kind == permissions.KindInputMonitoring && state == permissions.Granted {
		if err := c.syncHotkey(ctx, true); err != nil {
			c.hotkeyRestartRequired.Store(true)
			return apperr.New(apperr.CodeHotkeyUnavailable, "enable global hotkey", fmt.Errorf("%v; restart Waydict to retry", err))
		}
	}
	return nil
}

func (c *appController) pollStatus(ctx context.Context) {
	var previous appkit.ViewModel
	havePrevious := false
	for {
		view := c.currentView(ctx)
		granted := view.InputMonitoring == string(permissions.Granted)
		enabled, _, bindingErr := c.runtime.App.HotkeyBinding()
		if !enabled {
			c.hotkeyRestartRequired.Store(false)
		}
		if granted && enabled && bindingErr == nil && c.runtime.Platform.Hotkey != nil {
			hotkeyStatus := c.runtime.Platform.Hotkey.Status()
			if !hotkeyStatus.Running && hotkeyStatus.LastErrorCode != "" && hotkeyStatus.LastErrorCode != apperr.CodePermissionInputMonitoringDenied {
				c.hotkeyRestartRequired.Store(true)
			}
			if !hotkeyStatus.Running && !c.hotkeyRestartRequired.Load() {
				if err := c.syncHotkey(ctx, true); err != nil {
					c.hotkeyRestartRequired.Store(true)
					c.host.ShowError(apperr.Code(err), fmt.Sprintf("%v; restart Waydict to retry", err))
				}
			}
		}
		if !havePrevious || !reflect.DeepEqual(view, previous) {
			_ = c.host.Update(view)
			previous = view
			havePrevious = true
		}
		interval := 500 * time.Millisecond
		if view.State != string(api.StateIdle) {
			interval = 100 * time.Millisecond
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (c *appController) currentView(ctx context.Context) appkit.ViewModel {
	status := c.runtime.App.Status(ctx)
	view := c.baseView()
	view.State = string(status.State)
	if status.LastError != nil {
		view.LastError = status.LastError.Message
	}
	if status.LastWarning != nil {
		view.LastWarning = status.LastWarning.Message
	}
	if status.Permissions != nil {
		view.MicrophonePermission = status.Permissions.Microphone
		view.Accessibility = status.Permissions.Accessibility
		view.InputMonitoring = status.Permissions.InputMonitoring
	}
	if status.Hotkey != nil {
		view.HotkeyMode = string(status.Hotkey.Mode)
		view.HotkeyAvailable = status.Hotkey.Available
		view.HotkeyDescription = hotkeyDescription(status.Hotkey.Modifiers, status.Hotkey.Key)
	}
	view.AudioDeviceName = status.Audio.DeviceName
	view.AudioDeviceID = status.Audio.DeviceID
	view.AudioDeviceControlled = c.cfg.IsExplicit("audio.device")
	if view.AudioDeviceControlled {
		view.SelectedAudioDevice = c.cfg.Audio.Device
	} else if c.runtime.Platform.Preferences != nil {
		preferenceCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
		selected, found, err := c.runtime.Platform.Preferences.String(preferenceCtx, preferences.KeySelectedAudioDeviceUID)
		cancel()
		if err == nil && found {
			view.SelectedAudioDevice = selected
		}
	}
	if c.runtime.Platform.DeviceManager != nil {
		deviceCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
		devices, err := c.runtime.Platform.DeviceManager.Devices(deviceCtx)
		cancel()
		if err == nil {
			view.AudioDevices = make([]appkit.AudioDevice, 0, len(devices))
			for _, device := range devices {
				view.AudioDevices = append(view.AudioDevices, appkit.AudioDevice{
					ID:        device.ID,
					Name:      device.Name,
					Default:   device.Default,
					Connected: device.Connected,
				})
			}
		}
	}
	view.AudioBackend = status.Audio.Backend
	view.InjectionBackend = status.Injection.Engine
	view.FocusBackend = status.Focus.Backend
	view.ASREngine = status.ASR.ResolvedEngine
	if view.ASREngine == "" {
		view.ASREngine = status.ASR.Engine
	}
	view.ASRModel = status.ASR.Model
	view.ASRProvider = status.ASR.ResolvedProvider
	if view.ASRProvider == "" {
		view.ASRProvider = status.ASR.Provider
	}
	view.PendingRestart = status.PendingRestart || c.hotkeyRestartRequired.Load()
	if c.hotkeyRestartRequired.Load() && view.LastWarning == "" {
		view.LastWarning = "Restart Waydict to retry the global shortcut."
	}
	if c.runtime.Platform.LoginItem != nil {
		loginCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
		enabled, err := c.runtime.Platform.LoginItem.Status(loginCtx)
		cancel()
		view.LaunchAtLogin = enabled
		if err != nil {
			view.LaunchAtLoginError = err.Error()
		}
	}
	return view
}

func (c *appController) baseView() appkit.ViewModel {
	configPath := c.cfg.ActivePath()
	if configPath == "" {
		configPath = c.cfg.Paths().ConfigFile
	}
	view := appkit.ViewModel{
		State:                string(api.StateArming),
		MicrophonePermission: string(permissions.Unavailable),
		Accessibility:        string(permissions.Unavailable),
		InputMonitoring:      string(permissions.Unavailable),
		HotkeyMode:           c.cfg.Hotkey.Mode,
		HotkeyDescription:    hotkeyDescription(c.cfg.Hotkey.Modifiers, c.cfg.Hotkey.Key),
		InstallingModels:     c.installing.Load(),
		Version:              buildinfo.Version,
		Commit:               buildinfo.Commit,
		BuildNumber:          buildinfo.BuildNumber,
		BuildTags:            buildinfo.BuildTags,
		Architecture:         runtime.GOARCH,
		Platform:             "darwin",
		ConfigPath:           configPath,
		LegacyConfig:         c.cfg.LegacyPathActive(),
		MigrationWarning:     strings.Join(c.cfg.MigrationWarnings(), "; "),
		SocketPath:           c.cfg.Daemon.Socket,
	}
	view.ModelsReady = c.modelsReady.Load()
	if status := c.modelStatus.Load(); status != nil {
		view.ModelStatus, _ = status.(string)
	}
	return view
}

func hotkeyDescription(modifiers []string, key string) string {
	var value strings.Builder
	for _, modifier := range modifiers {
		switch modifier {
		case "control":
			value.WriteString("⌃")
		case "shift":
			value.WriteString("⇧")
		case "option":
			value.WriteString("⌥")
		case "command":
			value.WriteString("⌘")
		}
	}
	if strings.EqualFold(key, "space") {
		value.WriteString("Space")
	} else if strings.HasPrefix(key, "keycode:") {
		value.WriteString(key)
	} else {
		value.WriteString(strings.ToUpper(key))
	}
	return value.String()
}

func (c *appController) installRequiredModels(ctx context.Context) error {
	if !c.installing.CompareAndSwap(false, true) {
		return apperr.New(apperr.CodeModelInstallBusy, "install models", errors.New("model installation is already running"))
	}
	defer func() {
		c.checkModels()
		c.installing.Store(false)
	}()
	modelsDir := c.cfg.Paths().ModelsDir
	if c.cfg.ASR.Engine == "auto" || c.cfg.ASR.Engine == "whisper-cpp" {
		if _, err := modelinstall.InstallWhisper(ctx, c.cfg.ASR.WhisperModel, modelinstall.InstallOptions{Dir: modelsDir}); err != nil {
			return err
		}
	}
	if c.cfg.ASR.Engine == "auto" || c.cfg.ASR.Engine == "sherpa-onnx" {
		if strings.Contains(c.cfg.ASR.ModelDir, "v3-int8") {
			if _, err := modelinstall.InstallParakeetV3Int8(ctx, modelinstall.InstallOptions{Dir: modelsDir}); err != nil {
				return err
			}
		} else if _, err := modelinstall.InstallParakeetUnifiedFP32(ctx, modelinstall.InstallOptions{Dir: modelsDir}); err != nil {
			return err
		}
	}
	if c.cfg.VAD.Engine == "silero" {
		if _, err := modelinstall.InstallSileroVAD(ctx, modelinstall.InstallOptions{Dir: modelsDir}); err != nil {
			return err
		}
	}
	return nil
}

func (c *appController) checkModels() {
	result := model.CheckConfig(c.cfg, model.CheckOptions{StrictSizes: true})
	ready := result.OK
	if c.cfg.VAD.Engine == "silero" {
		info, err := os.Stat(c.cfg.VAD.Model)
		ready = ready && err == nil && info.Mode().IsRegular() && info.Size() >= model.MinSileroVADSize
	}
	c.modelsReady.Store(ready)
	if ready {
		c.modelStatus.Store("ready")
	} else {
		c.modelStatus.Store("missing_or_invalid")
	}
}

type hostPresenter struct{ host *appkit.Host }

func (p hostPresenter) Open(ctx context.Context, path string) error {
	return p.host.OpenPath(ctx, path)
}
func (p hostPresenter) Reveal(ctx context.Context, path string) error {
	return p.host.RevealPath(ctx, path)
}

func (c *appController) openConfig(ctx context.Context) error {
	_, _, err := config.OpenConfigForEditing(ctx, c.cfg.ActivePath(), c.cfg.Paths(), hostPresenter{host: c.host})
	return err
}

func (c *appController) openLog(ctx context.Context) error {
	path := c.cfg.Paths().LogFile
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return c.host.OpenPath(ctx, path)
}

func (c *appController) diagnosticsData(ctx context.Context) (map[string]any, error) {
	view := c.baseView()
	if c.runtime != nil {
		view = c.currentView(ctx)
	}
	return map[string]any{
		"version":           view.Version,
		"commit":            view.Commit,
		"build_number":      view.BuildNumber,
		"build_tags":        view.BuildTags,
		"architecture":      view.Architecture,
		"platform":          view.Platform,
		"bundle_path":       redactHome(c.installation.BundlePath),
		"translocated":      c.installation.Translocated,
		"volume_read_only":  c.installation.ReadOnly,
		"config_path":       redactHome(view.ConfigPath),
		"runtime_state":     view.State,
		"microphone":        view.MicrophonePermission,
		"accessibility":     view.Accessibility,
		"input_monitoring":  view.InputMonitoring,
		"launch_at_login":   view.LaunchAtLogin,
		"login_item_error":  view.LaunchAtLoginError,
		"audio_backend":     view.AudioBackend,
		"injection_backend": view.InjectionBackend,
		"focus_backend":     view.FocusBackend,
		"asr_engine":        view.ASREngine,
		"asr_model":         view.ASRModel,
		"asr_provider":      view.ASRProvider,
		"model_status":      view.ModelStatus,
		"last_error":        redactHome(view.LastError),
		"last_warning":      redactHome(view.LastWarning),
	}, nil
}

func redactHome(path string) string {
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		return strings.ReplaceAll(path, home, "~")
	}
	return path
}

func (c *appController) runWithoutRuntime(ctx context.Context, startupErr error, translocated bool) {
	if c.cfg.Paths().ConfigFile == "" {
		c.cfg = config.DefaultsFor("darwin", config.CurrentPlatformPaths())
	}
	view := c.baseView()
	view.State = string(api.StateError)
	view.LastError = startupErr.Error()
	view.InstallationBlocked = translocated
	if translocated {
		view.InstallationMessage = moveMessage
	}
	_ = c.host.Update(view)
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-c.host.Events():
			switch event.Action {
			case appkit.ActionQuit:
				c.cancel()
			case appkit.ActionRunDiagnostics:
				c.host.ShowDiagnostics(false)
			case appkit.ActionCopyDiagnostics:
				c.host.ShowDiagnostics(true)
			case appkit.ActionOpenConfig:
				if translocated {
					c.host.ShowError(apperr.CodeAppTranslocated, startupErr.Error())
				} else if err := c.openConfig(ctx); err != nil {
					c.host.ShowError(apperr.Code(err), err.Error())
				}
			default:
				code := apperr.Code(startupErr)
				if translocated {
					code = apperr.CodeAppTranslocated
				}
				c.host.ShowError(code, startupErr.Error())
			}
		}
	}
}
