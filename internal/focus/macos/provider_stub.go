//go:build !darwin || !cgo

package macos

import (
	"context"
	"fmt"

	"waydict/internal/apperr"
	"waydict/internal/focus"
)

type Provider struct{}

func New() *Provider { return &Provider{} }

func (p *Provider) Backend() string { return "accessibility" }

func (p *Provider) Available(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return unavailable("check Accessibility focus")
}

func (p *Provider) Current(ctx context.Context) (focus.Target, error) {
	if err := ctx.Err(); err != nil {
		return focus.Target{}, err
	}
	return focus.Target{}, unavailable("read Accessibility focus")
}

func (p *Provider) Same(ctx context.Context, _ focus.Target) (focus.Target, bool, error) {
	if err := ctx.Err(); err != nil {
		return focus.Target{}, false, err
	}
	return focus.Target{}, false, unavailable("compare Accessibility focus")
}

func (p *Provider) Release(focus.Target) {}

func (p *Provider) Close() error { return nil }

func unavailable(operation string) error {
	return apperr.New(apperr.CodeFocusUnavailable, operation, fmt.Errorf("Accessibility focus requires Darwin with cgo"))
}

var _ focus.Provider = (*Provider)(nil)
