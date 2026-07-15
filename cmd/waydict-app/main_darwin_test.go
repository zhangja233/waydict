//go:build darwin && cgo

package main

import (
	"context"
	"testing"
	"time"

	"waydict/internal/app"
	"waydict/internal/audio"
	"waydict/internal/config"
	"waydict/internal/hotkey"
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
