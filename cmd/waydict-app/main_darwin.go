//go:build darwin && cgo

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"waydict/internal/app"
	"waydict/internal/apperr"
	"waydict/internal/buildinfo"
	"waydict/internal/config"
	"waydict/internal/control"
	"waydict/internal/diagnostics"
	"waydict/internal/hotkey"
	svlog "waydict/internal/log"
	"waydict/internal/macos/appkit"
	"waydict/internal/model"
	"waydict/internal/modelinstall"
	"waydict/internal/networkpolicy"
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
	rootCtx               context.Context
	installing            atomic.Bool
	installMu             sync.Mutex
	installStatus         api.ModelInstallStatus
	installCancel         context.CancelFunc
	installWG             sync.WaitGroup
	modelsReady           atomic.Bool
	modelStatus           atomic.Value
	hotkeyEvents          chan hotkey.Event
	hotkeyHold            uint64
	hotkeyMu              sync.Mutex
	hotkeyRestartRequired atomic.Bool
	sleeping              bool
	sessionInactive       bool
	suspended             atomic.Bool
	startupError          *api.ErrorInfo
}

func (c *appController) run(ctx context.Context) {
	defer c.host.Terminate()
	c.rootCtx = ctx
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
	var logOutput io.Writer = os.Stderr
	logWriter, logErr := svlog.OpenRotating(cfg.Paths().LogFile, svlog.DefaultMaxBytes, svlog.DefaultBackups)
	if logErr == nil {
		defer logWriter.Close()
		logOutput = io.MultiWriter(os.Stderr, logWriter)
	} else {
		fmt.Fprintln(os.Stderr, logErr)
	}

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
	cleanupLock, cleanupErr := modelinstall.Acquire(ctx, modelinstall.InstallOptions{Dir: cfg.Paths().ModelsDir, StateDir: cfg.Paths().StateDir, CacheDir: cfg.Paths().CacheDir})
	if cleanupErr == nil {
		_ = cleanupLock.Close()
	} else if apperr.Code(cleanupErr) != apperr.CodeModelInstallBusy {
		fmt.Fprintln(os.Stderr, cleanupErr)
	}
	c.checkModels()

	c.services = platform.Current()
	dependencies := c.platformDependencies()
	rt, err := app.NewRuntime(ctx, cfg, app.RuntimeOptions{
		Platform:             dependencies,
		AllowDegradedStartup: true,
		NewWhisper:           appWhisperFactory,
		ProbeAccelerator:     appAcceleratorProbe,
		LogOutput:            logOutput,
		Shutdown:             c.cancel,
	})
	if err != nil {
		c.runWithoutRuntime(ctx, err, false)
		return
	}
	c.runtime = rt
	if logErr != nil {
		c.host.ShowError(apperr.CodeInternalError, fmt.Sprintf("open protected app log: %v", logErr))
	}
	c.hotkeyEvents = make(chan hotkey.Event, 64)

	serveResults := make(chan error, 1)
	go func() {
		serveResults <- rt.Serve(ctx)
	}()
	serveStopped := false
	defer func() {
		c.cancel()
		installDone := make(chan struct{})
		go func() {
			c.installWG.Wait()
			close(installDone)
		}()
		select {
		case <-installDone:
		case <-time.After(3 * time.Second):
			fmt.Fprintln(os.Stderr, "timed out waiting for model installation shutdown")
		}
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
	if err := c.syncHotkey(ctx, false); err != nil && apperr.Code(err) != apperr.CodePermissionAccessibilityDenied {
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
	dependencies.HostActions.InstallRequiredModels = c.startModelInstall
	dependencies.HostActions.CancelModelInstall = c.cancelModelInstall
	dependencies.HostActions.ModelInstallStatus = c.modelInstallStatus
	dependencies.HostActions.RevealModels = func(ctx context.Context) error {
		cfg := c.cfg
		if c.runtime != nil && c.runtime.App != nil {
			cfg = c.runtime.App.ConfigSnapshot()
		}
		path := cfg.Paths().ModelsDir
		if err := os.MkdirAll(path, 0700); err != nil {
			return err
		}
		return c.host.RevealPath(ctx, path)
	}
	dependencies.HostActions.OpenConfig = c.openConfig
	dependencies.HostActions.OpenLog = c.openLog
	dependencies.HostActions.RunDiagnostics = c.diagnosticsData
	dependencies.HostActions.CopyDiagnostics = func(ctx context.Context) error {
		report, err := c.diagnosticsReport(ctx)
		if err != nil {
			return err
		}
		c.host.ShowDiagnostics(report, true)
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
		return c.startModelInstallProfile(ctx, event.Payload)
	case appkit.ActionCancelModelInstall:
		return c.cancelModelInstall(ctx)
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
		var hotkeyErr error
		if c.runtime.Platform.Hotkey != nil && !c.runtime.Platform.Hotkey.Status().Running {
			hotkeyErr = c.syncHotkey(ctx, true)
		}
		report, err := c.diagnosticsReport(ctx)
		if err != nil {
			return err
		}
		c.host.ShowDiagnostics(report, false)
		return hotkeyErr
	case appkit.ActionCopyDiagnostics:
		report, err := c.diagnosticsReport(ctx)
		if err != nil {
			return err
		}
		c.host.ShowDiagnostics(report, true)
		return nil
	case appkit.ActionSystemWillSleep:
		return c.setLifecycleState(ctx, true, c.sessionInactive)
	case appkit.ActionSystemDidWake:
		return c.setLifecycleState(ctx, false, c.sessionInactive)
	case appkit.ActionSessionDidResignActive:
		return c.setLifecycleState(ctx, c.sleeping, true)
	case appkit.ActionSessionDidBecomeActive:
		return c.setLifecycleState(ctx, c.sleeping, false)
	case appkit.ActionMemoryPressureCritical:
		_, err := c.runtime.ReleaseASRForMemoryPressure()
		return err
	case appkit.ActionQuit:
		c.cancel()
		return nil
	default:
		return fmt.Errorf("unsupported AppKit action %d", event.Action)
	}
}

func (c *appController) setLifecycleState(ctx context.Context, sleeping, sessionInactive bool) error {
	c.sleeping = sleeping
	c.sessionInactive = sessionInactive
	wantSuspended := sleeping || sessionInactive
	if wantSuspended == c.suspended.Load() {
		return nil
	}
	c.suspended.Store(wantSuspended)
	if wantSuspended {
		var errs []error
		if c.runtime.Platform.Hotkey != nil {
			if err := c.runtime.Platform.Hotkey.Stop(ctx); err != nil {
				errs = append(errs, err)
			}
		}
		if err := c.runtime.Suspend(ctx); err != nil {
			errs = append(errs, err)
		}
		c.hotkeyHold = 0
		return errors.Join(errs...)
	}
	return c.recoverServices(ctx)
}

func (c *appController) recoverServices(ctx context.Context) error {
	var errs []error
	if c.runtime.Platform.PermissionSource != nil {
		if _, err := c.runtime.Platform.PermissionSource.Snapshot(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if c.runtime.Platform.DeviceManager != nil {
		if _, err := c.runtime.Platform.DeviceManager.Devices(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if err := c.runtime.RecreateAudio(ctx); err != nil {
		errs = append(errs, err)
	}
	if c.runtime.Platform.Hotkey != nil {
		if err := c.runtime.Platform.Hotkey.Stop(ctx); err != nil {
			errs = append(errs, err)
		} else if err := c.syncHotkey(ctx, true); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
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
	if snapshot.Accessibility != permissions.Granted {
		return apperr.New(apperr.CodePermissionAccessibilityDenied, "configure global hotkey", errors.New("Accessibility permission is not granted"))
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
		granted := view.Accessibility == string(permissions.Granted)
		enabled, _, bindingErr := c.runtime.App.HotkeyBinding()
		if !enabled {
			c.hotkeyRestartRequired.Store(false)
		}
		if !c.suspended.Load() && granted && enabled && bindingErr == nil && c.runtime.Platform.Hotkey != nil {
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
	cfg := c.runtime.App.ConfigSnapshot()
	view := c.baseView()
	view.State = string(status.State)
	if status.LastError != nil {
		view.LastErrorCode = status.LastError.Code
		view.LastError = status.LastError.Message
	}
	if status.LastWarning != nil {
		view.LastWarningCode = status.LastWarning.Code
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
	view.HotkeyModeControlled = cfg.IsExplicit("hotkey.mode")
	view.AudioDeviceName = status.Audio.DeviceName
	view.AudioDeviceID = status.Audio.DeviceID
	view.AudioDeviceControlled = cfg.IsExplicit("audio.device")
	if view.AudioDeviceControlled {
		view.SelectedAudioDevice = cfg.Audio.Device
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
	if status.ModelInstall != nil {
		view.InstallingModels = status.ModelInstall.Running
		view.ModelInstallItem = status.ModelInstall.Item
		view.ModelInstallPhase = status.ModelInstall.Phase
		view.ModelInstallPercent = status.ModelInstall.Percent
		if status.ModelInstall.Error != nil {
			view.ModelInstallError = status.ModelInstall.Error.Code + ": " + status.ModelInstall.Error.Message
		}
	}
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
	cfg := c.cfg
	if c.runtime != nil && c.runtime.App != nil {
		cfg = c.runtime.App.ConfigSnapshot()
	}
	configPath := cfg.ActivePath()
	if configPath == "" {
		configPath = cfg.Paths().ConfigFile
	}
	view := appkit.ViewModel{
		State:                string(api.StateArming),
		MicrophonePermission: string(permissions.Unavailable),
		Accessibility:        string(permissions.Unavailable),
		InputMonitoring:      string(permissions.Unavailable),
		HotkeyMode:           cfg.Hotkey.Mode,
		HotkeyDescription:    hotkeyDescription(cfg.Hotkey.Modifiers, cfg.Hotkey.Key),
		HotkeyModeControlled: cfg.IsExplicit("hotkey.mode"),
		InstallingModels:     c.installing.Load(),
		Version:              buildinfo.Version,
		Commit:               buildinfo.Commit,
		BuildNumber:          buildinfo.BuildNumber,
		BuildTags:            buildinfo.BuildTags,
		Architecture:         runtime.GOARCH,
		Platform:             "darwin",
		ConfigPath:           configPath,
		LegacyConfig:         cfg.LegacyPathActive(),
		MigrationWarning:     strings.Join(cfg.MigrationWarnings(), "; "),
		SocketPath:           cfg.Daemon.Socket,
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

func (c *appController) startModelInstall(ctx context.Context) error {
	return c.startModelInstallProfile(ctx, "required")
}

func (c *appController) startModelInstallProfile(ctx context.Context, profile string) error {
	if profile == "" {
		profile = "required"
	}
	if profile != "required" && profile != "recommended" && profile != "cpu" {
		return fmt.Errorf("invalid model installation profile %q", profile)
	}
	if c.runtime == nil || c.runtime.App == nil {
		return errors.New("runtime is unavailable")
	}
	status := c.runtime.App.Status(ctx)
	if status.State != api.StateIdle && status.State != api.StateError {
		return apperr.New(apperr.CodeModelInstallBusy, "install models", errors.New("dictation must be idle before installing models"))
	}
	if !c.installing.CompareAndSwap(false, true) {
		return apperr.New(apperr.CodeModelInstallBusy, "install models", errors.New("model installation is already running in this Waydict process"))
	}
	cfg := c.runtime.App.ConfigSnapshot()
	opts := modelinstall.InstallOptions{
		Dir:      cfg.Paths().ModelsDir,
		StateDir: cfg.Paths().StateDir,
		CacheDir: cfg.Paths().CacheDir,
		Progress: c.updateModelInstallProgress,
	}
	lock, err := modelinstall.Acquire(ctx, opts)
	if err != nil {
		c.installing.Store(false)
		return err
	}
	installRoot := c.rootCtx
	if installRoot == nil {
		installRoot = context.Background()
	}
	installCtx, cancel := context.WithCancel(installRoot)
	c.installMu.Lock()
	c.installCancel = cancel
	c.installStatus = api.ModelInstallStatus{Running: true, Cancellable: true, Phase: "resolving"}
	c.installMu.Unlock()
	c.installWG.Add(1)
	go func() {
		defer c.installWG.Done()
		defer lock.Close()
		err := c.runRequiredModelInstall(installCtx, cfg, profile, lock.LockedOptions(opts))
		c.checkModels()
		if err == nil && !c.modelsReady.Load() {
			err = apperr.New(apperr.CodeASRModelInvalid, "install models", errors.New("strict model checks failed after installation"))
		}
		if err == nil {
			err = c.runtime.RestartASR(installCtx)
		}
		c.finishModelInstall(err)
		if err != nil && !errors.Is(err, context.Canceled) {
			c.host.ShowError(apperr.Code(err), err.Error())
		}
	}()
	return nil
}

func (c *appController) runRequiredModelInstall(ctx context.Context, cfg config.Config, profile string, opts modelinstall.InstallOptions) error {
	installWhisper := profile != "cpu" && (cfg.ASR.Engine == "auto" || cfg.ASR.Engine == "whisper-cpp")
	installSherpa := profile == "cpu" || cfg.ASR.Engine == "sherpa-onnx"
	if installWhisper {
		if _, err := modelinstall.InstallWhisper(ctx, cfg.ASR.WhisperModel, opts); err != nil {
			return err
		}
	}
	if installSherpa {
		if strings.Contains(cfg.ASR.ModelDir, "v3-int8") {
			if _, err := modelinstall.InstallParakeetV3Int8(ctx, opts); err != nil {
				return err
			}
		} else if _, err := modelinstall.InstallParakeetUnifiedFP32(ctx, opts); err != nil {
			return err
		}
	}
	if cfg.VAD.Engine == "silero" {
		if _, err := modelinstall.InstallSileroVAD(ctx, opts); err != nil {
			return err
		}
	}
	return nil
}

func (c *appController) updateModelInstallProgress(progress modelinstall.Progress) {
	c.installMu.Lock()
	defer c.installMu.Unlock()
	c.installStatus.Running = true
	c.installStatus.Cancellable = true
	c.installStatus.Item = progress.Item
	c.installStatus.Phase = progress.Phase
	c.installStatus.BytesDownloaded = progress.BytesDownloaded
	c.installStatus.TotalBytes = progress.TotalBytes
	c.installStatus.Percent = 0
	if progress.TotalBytes > 0 {
		c.installStatus.Percent = min(100, float64(progress.BytesDownloaded)*100/float64(progress.TotalBytes))
	}
}

func (c *appController) finishModelInstall(err error) {
	c.installMu.Lock()
	c.installCancel = nil
	c.installStatus.Running = false
	c.installStatus.Cancellable = false
	if err == nil {
		c.installStatus.Phase = "complete"
		c.installStatus.Percent = 100
		c.installStatus.Error = nil
	} else if errors.Is(err, context.Canceled) {
		c.installStatus.Phase = "cancelled"
		c.installStatus.Error = nil
	} else {
		c.installStatus.Phase = "failed"
		c.installStatus.Error = &api.ErrorInfo{Code: apperr.Code(err), Message: err.Error()}
	}
	c.installMu.Unlock()
	c.installing.Store(false)
}

func (c *appController) cancelModelInstall(context.Context) error {
	c.installMu.Lock()
	cancel := c.installCancel
	c.installMu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

func (c *appController) modelInstallStatus() *api.ModelInstallStatus {
	c.installMu.Lock()
	defer c.installMu.Unlock()
	status := c.installStatus
	if !status.Running && status.Phase == "" && status.Error == nil {
		return nil
	}
	if status.Error != nil {
		copy := *status.Error
		status.Error = &copy
	}
	return &status
}

func (c *appController) checkModels() {
	cfg := c.cfg
	if c.runtime != nil && c.runtime.App != nil {
		cfg = c.runtime.App.ConfigSnapshot()
	}
	result := model.CheckConfig(cfg, model.CheckOptions{StrictSizes: true})
	ready := result.OK
	if cfg.VAD.Engine == "silero" {
		info, err := os.Stat(cfg.VAD.Model)
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
	cfg := c.cfg
	if c.runtime != nil && c.runtime.App != nil {
		cfg = c.runtime.App.ConfigSnapshot()
	}
	_, _, err := config.OpenConfigForEditing(ctx, cfg.ActivePath(), cfg.Paths(), hostPresenter{host: c.host})
	return err
}

func (c *appController) openLog(ctx context.Context) error {
	cfg := c.cfg
	if c.runtime != nil && c.runtime.App != nil {
		cfg = c.runtime.App.ConfigSnapshot()
	}
	path := cfg.Paths().LogFile
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing symlink log path %s", path)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Chmod(path, 0600); err != nil {
		return err
	}
	return c.host.OpenPath(ctx, path)
}

func (c *appController) diagnosticsData(ctx context.Context) (map[string]any, error) {
	if c.runtime != nil && c.runtime.Platform.Hotkey != nil && !c.runtime.Platform.Hotkey.Status().Running {
		if err := c.syncHotkey(ctx, true); err != nil {
			c.host.ShowError(apperr.Code(err), err.Error())
		}
	}
	output, err := c.diagnosticsOutput(ctx)
	return output.Data, err
}

func (c *appController) diagnosticsReport(ctx context.Context) (string, error) {
	output, err := c.diagnosticsOutput(ctx)
	return output.Text, err
}

func (c *appController) diagnosticsOutput(ctx context.Context) (diagnostics.Output, error) {
	cfg := c.cfg
	status := api.Status{State: api.StateError}
	if c.runtime != nil && c.runtime.App != nil {
		cfg = c.runtime.App.ConfigSnapshot()
		status = c.runtime.App.Status(ctx)
	} else if c.startupError != nil {
		copy := *c.startupError
		status.LastError = &copy
	}
	permissionsSnapshot := api.PermissionStatus{}
	if status.Permissions != nil {
		permissionsSnapshot = *status.Permissions
	}
	devices := []appkit.AudioDevice{}
	enumerationStatus := "unavailable"
	if c.runtime != nil && c.runtime.Platform.DeviceManager != nil {
		deviceCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		listed, err := c.runtime.Platform.DeviceManager.Devices(deviceCtx)
		cancel()
		if err != nil {
			enumerationStatus = err.Error()
		} else {
			enumerationStatus = "ok"
			for _, device := range listed {
				devices = append(devices, appkit.AudioDevice{ID: device.ID, Name: device.Name, Default: device.Default, Connected: device.Connected})
			}
		}
	}
	defaultID, defaultName := "", ""
	for _, device := range devices {
		if device.Default {
			defaultName = device.Name
			if device.ID == status.Audio.DeviceID {
				defaultID = device.ID
			}
			break
		}
	}
	modelCheck := model.CheckConfig(cfg, model.CheckOptions{StrictSizes: true})
	asrCheck := "ready"
	if !modelCheck.OK {
		asrCheck = "missing_or_invalid"
	}
	vadCheck := model.CheckVADConfig(cfg)
	vadStatus := "ready"
	if vadCheck.Warning != "" {
		vadStatus = vadCheck.Warning
	}
	socket := diagnostics.SocketSnapshot{Path: cfg.Daemon.Socket, OwnerUID: -1, Mode: "unavailable", ConnectionTest: "not connected"}
	if info, err := os.Lstat(cfg.Daemon.Socket); err == nil {
		socket.Mode = fmt.Sprintf("%04o", info.Mode().Perm())
		if stat, ok := info.Sys().(*syscall.Stat_t); ok {
			socket.OwnerUID = int(stat.Uid)
		}
	}
	if c.runtime != nil {
		probeCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		response, err := control.Send(probeCtx, cfg.Daemon.Socket, control.NewRequest("status", nil))
		cancel()
		if err != nil {
			socket.ConnectionTest = err.Error()
		} else if !response.OK {
			socket.ConnectionTest = response.Error.Code
		} else {
			socket.ConnectionTest = "ok"
		}
	}
	recentLogs, logErr := svlog.TailLines(cfg.Paths().LogFile, 100)
	if logErr != nil {
		recentLogs = []string{"log tail unavailable: " + logErr.Error()}
	}
	resolvedEngine := status.ASR.ResolvedEngine
	if resolvedEngine == "" {
		resolvedEngine = status.ASR.Engine
	}
	resolvedProvider := status.ASR.ResolvedProvider
	if resolvedProvider == "" {
		resolvedProvider = status.ASR.Provider
	}
	modelStatus := "missing_or_invalid"
	if c.modelsReady.Load() {
		modelStatus = "ready"
	}
	configPath := cfg.ActivePath()
	if configPath == "" {
		configPath = cfg.Paths().ConfigFile
	}
	home, _ := os.UserHomeDir()
	snapshot := diagnostics.Snapshot{
		Version:           buildinfo.Version,
		Commit:            buildinfo.Commit,
		BuildNumber:       buildinfo.BuildNumber,
		BuildTags:         buildinfo.BuildTags,
		Architecture:      runtime.GOARCH,
		Platform:          "darwin",
		OSVersion:         c.installation.OSVersion,
		BundlePath:        c.installation.BundlePath,
		Translocated:      c.installation.Translocated,
		VolumeReadOnly:    c.installation.ReadOnly,
		ConfigPath:        configPath,
		LegacyConfig:      cfg.LegacyPathActive(),
		MigrationWarnings: cfg.MigrationWarnings(),
		RuntimeState:      string(status.State),
		CompiledFeatures: map[string]bool{
			"coreaudio": buildinfo.CoreAudioEnabled,
			"quartz":    buildinfo.MacOSNativeEnabled,
			"focus_ax":  buildinfo.MacOSNativeEnabled,
			"event_tap": buildinfo.MacOSNativeEnabled && c.services.Capabilities.HotkeyAvailable,
			"sherpa":    buildinfo.SherpaEnabled,
			"whisper":   buildinfo.WhisperEnabled,
		},
		ResolvedBackends: map[string]string{"audio": status.Audio.Backend, "focus": status.Focus.Backend, "injection": status.Injection.Engine, "asr": resolvedEngine, "provider": resolvedProvider},
		Permissions:      permissionsSnapshot,
		Audio: diagnostics.AudioSnapshot{
			Backend: status.Audio.Backend, SelectedDeviceID: status.Audio.DeviceID, SelectedDevice: status.Audio.DeviceName,
			DefaultDeviceID: defaultID, DefaultDevice: defaultName, SampleRate: status.Audio.SampleRate, Capturing: status.Audio.Capturing,
			Overruns: status.Audio.Overruns, InputLatencyMS: status.Audio.InputLatencyMS, EnumerationStatus: enumerationStatus,
		},
		ASR: diagnostics.ASRSnapshot{
			ConfiguredEngine: status.ASR.Engine, ResolvedEngine: resolvedEngine, ConfiguredProvider: status.ASR.Provider,
			ResolvedProvider: resolvedProvider, Model: status.ASR.Model, GPUName: status.ASR.GPUName, Loaded: status.ASR.Loaded, Check: asrCheck,
		},
		VAD:                diagnostics.VADSnapshot{Engine: status.VAD.Engine, Model: filepath.Base(cfg.VAD.Model), Check: vadStatus},
		Socket:             socket,
		ModelStatus:        modelStatus,
		LastError:          status.LastError,
		LastWarning:        status.LastWarning,
		SigningStatus:      signingStatus(ctx, c.installation.BundlePath),
		NotarizationStatus: "not evaluated in development build",
		QuarantineStatus:   quarantineStatus(c.installation.BundlePath),
		NetworkAllowlist:   append([]string(nil), networkpolicy.AllowedOutboundOperations...),
		RecentLogLines:     recentLogs,
	}
	return diagnostics.Build(snapshot, home), nil
}

func signingStatus(ctx context.Context, bundlePath string) string {
	if bundlePath == "" {
		return "bundle unavailable"
	}
	commandCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	output, err := exec.CommandContext(commandCtx, "/usr/bin/codesign", "--verify", "--deep", "--strict", bundlePath).CombinedOutput()
	detail := strings.TrimSpace(strings.ReplaceAll(string(output), "\n", "; "))
	if len(detail) > 1024 {
		detail = detail[:1024]
	}
	if err != nil {
		if detail == "" {
			detail = err.Error()
		}
		return "verification failed: " + detail
	}
	return "valid"
}

func quarantineStatus(bundlePath string) string {
	if bundlePath == "" {
		return "bundle unavailable"
	}
	_, err := unix.Getxattr(bundlePath, "com.apple.quarantine", nil)
	if err == nil {
		return "present"
	}
	if errors.Is(err, unix.ENOATTR) {
		return "absent"
	}
	return "unavailable: " + err.Error()
}

func (c *appController) runWithoutRuntime(ctx context.Context, startupErr error, translocated bool) {
	if c.cfg.Paths().ConfigFile == "" {
		c.cfg = config.DefaultsFor("darwin", config.CurrentPlatformPaths())
	}
	view := c.baseView()
	view.State = string(api.StateError)
	view.LastErrorCode = apperr.Code(startupErr)
	view.LastError = startupErr.Error()
	c.startupError = &api.ErrorInfo{Code: view.LastErrorCode, Message: view.LastError}
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
				report, _ := c.diagnosticsReport(ctx)
				c.host.ShowDiagnostics(report, false)
			case appkit.ActionCopyDiagnostics:
				report, _ := c.diagnosticsReport(ctx)
				c.host.ShowDiagnostics(report, true)
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
