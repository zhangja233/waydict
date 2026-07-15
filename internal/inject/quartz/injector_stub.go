//go:build !darwin || !cgo

package quartz

import (
	"context"
	"fmt"

	"waydict/internal/apperr"
	"waydict/internal/config"
	"waydict/internal/inject"
)

type Injector struct{}

func New(config.Injection) *Injector { return &Injector{} }

func (q *Injector) Backend() string { return "quartz" }

func (q *Injector) Available(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return unavailable("check Quartz injection")
}

func (q *Injector) Inject(ctx context.Context, _ inject.Request) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return unavailable("inject with Quartz")
}

func unavailable(operation string) error {
	return apperr.New(apperr.CodePermissionAccessibilityDenied, operation, fmt.Errorf("Quartz injection requires Darwin with cgo"))
}

var _ inject.Injector = (*Injector)(nil)
