package platform

import (
	"context"
	"fmt"

	"waydict/internal/apperr"
	"waydict/internal/audio"
	"waydict/internal/config"
	"waydict/internal/focus"
	"waydict/internal/inject"
)

func unavailableServices(name string) Services {
	return Services{
		Capabilities: Capabilities{OS: name, Host: "stub"},
		NewAudio: func(config.Audio) (audio.Source, error) {
			return nil, audio.ErrUnavailable
		},
		NewInjector: func(config.Injection) inject.Injector {
			return inject.Unavailable{Name: name}
		},
		NewFocus: func(config.Focus) focus.Provider {
			return unavailableFocus{backend: name}
		},
	}
}

type unavailableFocus struct {
	backend string
}

func (u unavailableFocus) Backend() string { return u.backend }
func (u unavailableFocus) Available(context.Context) error {
	return apperr.New(apperr.CodeFocusUnavailable, "check focus", fmt.Errorf("%s focus backend is unavailable", u.backend))
}
func (u unavailableFocus) Current(context.Context) (focus.Target, error) {
	return focus.Target{}, apperr.New(apperr.CodeFocusUnavailable, "read focus", fmt.Errorf("%s focus backend is unavailable", u.backend))
}
func (u unavailableFocus) Same(context.Context, focus.Target) (focus.Target, bool, error) {
	return focus.Target{}, false, apperr.New(apperr.CodeFocusUnavailable, "compare focus", fmt.Errorf("%s focus backend is unavailable", u.backend))
}
func (u unavailableFocus) Release(focus.Target) {}
