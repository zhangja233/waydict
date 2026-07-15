package platform

import (
	"waydict/internal/apphost"
	"waydict/internal/audio"
	"waydict/internal/config"
	"waydict/internal/focus"
	"waydict/internal/hotkey"
	"waydict/internal/inject"
	"waydict/internal/loginitem"
	"waydict/internal/permissions"
	"waydict/internal/preferences"
)

type Capabilities struct {
	OS                string
	Host              string
	AudioBackends     []string
	InjectionBackends []string
	FocusBackends     []string
	HotkeyAvailable   bool
	WhisperProviders  []string
	SherpaProviders   []string
}

type Services struct {
	Capabilities  Capabilities
	NewAudio      func(config.Audio) (audio.Source, error)
	Devices       audio.DeviceManager
	NewInjector   func(config.Injection) inject.Injector
	NewFocus      func(config.Focus) focus.Provider
	Permissions   permissions.Source
	Hotkey        hotkey.Service
	LoginItem     loginitem.Service
	Preferences   preferences.Store
	AppActivation apphost.Activator
}
