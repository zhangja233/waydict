package inject

import (
	"context"
	"fmt"
	"time"

	"waydict/internal/apperr"
	"waydict/internal/focus"
)

type Target struct {
	Focus focus.Target
}

type Request struct {
	Text           string
	Target         Target
	ValidateTarget func(context.Context, focus.Target) error
	KeyDelay       time.Duration
	Deadline       time.Time
}

type Injector interface {
	Backend() string
	Available(context.Context) error
	Inject(context.Context, Request) error
}

type Unavailable struct {
	Name string
}

func (u Unavailable) Backend() string { return u.Name }

func (u Unavailable) Available(context.Context) error {
	return apperr.New(apperr.CodeInjectorUnavailable, "check injector", fmt.Errorf("%s injector is unavailable", u.Name))
}

func (u Unavailable) Inject(context.Context, Request) error {
	return apperr.New(apperr.CodeInjectorUnavailable, "inject text", fmt.Errorf("%s injector is unavailable", u.Name))
}
