//go:build darwin && cgo

package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"waydict/internal/app"
	"waydict/internal/audio"
	"waydict/internal/config"
	"waydict/internal/hotkey"
	"waydict/internal/macos/appkit"
	"waydict/internal/permissions"
	"waydict/pkg/api"
)

type controllerHotkey struct {
	status hotkey.Status
}

func (f *controllerHotkey) Available(context.Context) error { return nil }
func (f *controllerHotkey) Start(context.Context, hotkey.Binding, hotkey.Handler) error {
	return nil
}
func (f *controllerHotkey) Rebind(_ context.Context, binding hotkey.Binding) error {
	f.status.Binding = binding
	return nil
}
func (f *controllerHotkey) Stop(context.Context) error { return nil }
func (f *controllerHotkey) Status() hotkey.Status      { return f.status }

func TestHotkeyHoldReleaseAndAbortOwnership(t *testing.T) {
	controller, application, service, cancel := newHotkeyController(t, hotkey.ModeHold)
	defer cancel()

	if err := controller.handleHotkeyEvent(context.Background(), hotkey.Event{Action: hotkey.Press}); err != nil {
		t.Fatal(err)
	}
	owned := controller.hotkeyHold
	if owned == 0 {
		t.Fatal("hold press did not own the started session")
	}
	if err := controller.handleHotkeyEvent(context.Background(), hotkey.Event{Action: hotkey.Release}); err != nil {
		t.Fatal(err)
	}
	waitForInactive(t, application)

	if err := controller.handleHotkeyEvent(context.Background(), hotkey.Event{Action: hotkey.Press}); err != nil {
		t.Fatal(err)
	}
	if err := application.Stop(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if err := application.Start(context.Background(), api.ModeToggle); err != nil {
		t.Fatal(err)
	}
	service.status.Binding.Mode = hotkey.ModeHold
	if err := controller.handleHotkeyEvent(context.Background(), hotkey.Event{Action: hotkey.Abort}); err != nil {
		t.Fatal(err)
	}
	if _, mode, active := application.ActiveSession(); !active || mode != api.ModeToggle {
		t.Fatalf("stale Abort changed replacement session: active=%t mode=%q", active, mode)
	}
	if err := application.Stop(context.Background(), false); err != nil {
		t.Fatal(err)
	}
}

func TestHotkeyToggleAndOneshotMapping(t *testing.T) {
	controller, application, service, cancel := newHotkeyController(t, hotkey.ModeToggle)
	defer cancel()

	if err := controller.handleHotkeyEvent(context.Background(), hotkey.Event{Action: hotkey.Press}); err != nil {
		t.Fatal(err)
	}
	if _, mode, active := application.ActiveSession(); !active || mode != api.ModeToggle {
		t.Fatalf("toggle press: active=%t mode=%q", active, mode)
	}
	if err := controller.handleHotkeyEvent(context.Background(), hotkey.Event{Action: hotkey.Press}); err != nil {
		t.Fatal(err)
	}
	waitForInactive(t, application)

	service.status.Binding.Mode = hotkey.ModeOneshot
	if err := controller.handleHotkeyEvent(context.Background(), hotkey.Event{Action: hotkey.Press}); err != nil {
		t.Fatal(err)
	}
	if _, mode, active := application.ActiveSession(); !active || mode != api.ModeOneshot {
		t.Fatalf("oneshot press: active=%t mode=%q", active, mode)
	}
	if err := application.Stop(context.Background(), false); err != nil {
		t.Fatal(err)
	}
}

func TestSleepAndSessionRecoveryWaitsForActiveSession(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := config.DefaultsFor("darwin", config.PlatformPaths{})
	cfg.Focus.Enabled = false
	first := &controllerRecoverySource{}
	second := &controllerRecoverySource{}
	var factoryCalls atomic.Int32
	permissionSource := &controllerPermissions{}
	devices := &controllerDevices{}
	hotkeyService := &controllerRecoveryHotkey{}
	application := app.New(ctx, cfg, app.Dependencies{
		Source: first,
		AudioSourceFactory: func(config.Audio) (audio.Source, error) {
			factoryCalls.Add(1)
			return second, nil
		},
		Engine:           &app.FakeEngine{IsLoaded: true},
		Injector:         &app.MemoryInjector{},
		PermissionSource: permissionSource,
		DeviceManager:    devices,
		Hotkey:           hotkeyService,
	})
	controller := &appController{runtime: &app.Runtime{App: application, Platform: app.PlatformDependencies{
		PermissionSource: permissionSource,
		DeviceManager:    devices,
		Hotkey:           hotkeyService,
	}}}
	if err := application.Start(ctx, api.ModeToggle); err != nil {
		t.Fatal(err)
	}
	if err := controller.handleEvent(ctx, appkit.Event{Action: appkit.ActionSystemWillSleep}); err != nil {
		t.Fatal(err)
	}
	if _, _, active := application.ActiveSession(); active || !first.stopped.Load() || !first.closed.Load() {
		t.Fatalf("sleep did not discard/release: active=%t stopped=%t closed=%t", active, first.stopped.Load(), first.closed.Load())
	}
	if err := controller.handleEvent(ctx, appkit.Event{Action: appkit.ActionSessionDidResignActive}); err != nil {
		t.Fatal(err)
	}
	if err := controller.handleEvent(ctx, appkit.Event{Action: appkit.ActionSystemDidWake}); err != nil {
		t.Fatal(err)
	}
	if factoryCalls.Load() != 0 {
		t.Fatal("wake rebuilt services while the login session was inactive")
	}
	if err := controller.handleEvent(ctx, appkit.Event{Action: appkit.ActionSessionDidBecomeActive}); err != nil {
		t.Fatal(err)
	}
	if factoryCalls.Load() != 1 || permissionSource.snapshots.Load() == 0 || devices.calls.Load() == 0 || hotkeyService.starts.Load() == 0 {
		t.Fatalf("recovery calls factory=%d permissions=%d devices=%d hotkey=%d", factoryCalls.Load(), permissionSource.snapshots.Load(), devices.calls.Load(), hotkeyService.starts.Load())
	}
}

func waitForInactive(t *testing.T, application *app.App) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		if _, _, active := application.ActiveSession(); !active {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("session did not become inactive")
		}
		time.Sleep(time.Millisecond)
	}
}

func newHotkeyController(t *testing.T, mode hotkey.Mode) (*appController, *app.App, *controllerHotkey, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cfg := config.Defaults()
	cfg.Focus.Enabled = false
	service := &controllerHotkey{status: hotkey.Status{
		Running: true,
		Binding: hotkey.Binding{Key: "space", KeyCode: 49, Modifiers: hotkey.ModifierCommand, Mode: mode},
	}}
	application := app.New(ctx, cfg, app.Dependencies{
		Source:   &audio.ScriptedSource{SampleRate: 16000, Delay: time.Millisecond},
		Engine:   &app.FakeEngine{IsLoaded: true},
		Injector: &app.MemoryInjector{},
		Hotkey:   service,
	})
	controller := &appController{
		runtime: &app.Runtime{
			App: application,
			Platform: app.PlatformDependencies{
				Hotkey: service,
			},
		},
	}
	return controller, application, service, cancel
}

var _ hotkey.Service = (*controllerHotkey)(nil)

type controllerRecoverySource struct {
	stopped atomic.Bool
	closed  atomic.Bool
}

func (*controllerRecoverySource) Start(context.Context) error { return nil }
func (*controllerRecoverySource) Pause(context.Context) error { return nil }
func (s *controllerRecoverySource) Stop(context.Context) error {
	s.stopped.Store(true)
	return nil
}
func (*controllerRecoverySource) Read(ctx context.Context, _ []float32) (int, error) {
	<-ctx.Done()
	return 0, ctx.Err()
}
func (*controllerRecoverySource) Stats() audio.Stats {
	return audio.Stats{Backend: "coreaudio", SampleRate: 16000}
}
func (s *controllerRecoverySource) Close() { s.closed.Store(true) }

type controllerPermissions struct{ snapshots atomic.Int32 }

func (p *controllerPermissions) Snapshot(context.Context) (permissions.Snapshot, error) {
	p.snapshots.Add(1)
	return permissions.Snapshot{Microphone: permissions.Granted, Accessibility: permissions.Granted, InputMonitoring: permissions.Granted}, nil
}
func (*controllerPermissions) Request(context.Context, permissions.Kind) (permissions.State, error) {
	return permissions.Granted, nil
}
func (*controllerPermissions) OpenSettings(context.Context, permissions.Kind) error { return nil }

type controllerDevices struct{ calls atomic.Int32 }

func (d *controllerDevices) Devices(context.Context) ([]audio.Device, error) {
	d.calls.Add(1)
	return []audio.Device{{ID: "default", Name: "Built-in", Default: true, Connected: true}}, nil
}

type controllerRecoveryHotkey struct {
	running atomic.Bool
	starts  atomic.Int32
	binding hotkey.Binding
}

func (*controllerRecoveryHotkey) Available(context.Context) error { return nil }
func (h *controllerRecoveryHotkey) Start(_ context.Context, binding hotkey.Binding, _ hotkey.Handler) error {
	h.binding = binding
	h.running.Store(true)
	h.starts.Add(1)
	return nil
}
func (h *controllerRecoveryHotkey) Rebind(_ context.Context, binding hotkey.Binding) error {
	h.binding = binding
	return nil
}
func (h *controllerRecoveryHotkey) Stop(context.Context) error {
	h.running.Store(false)
	return nil
}
func (h *controllerRecoveryHotkey) Status() hotkey.Status {
	return hotkey.Status{Running: h.running.Load(), Binding: h.binding}
}
