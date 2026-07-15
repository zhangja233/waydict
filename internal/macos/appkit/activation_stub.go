//go:build !darwin || !cgo

package appkit

import (
	"context"

	"waydict/internal/apphost"
)

type unavailableActivator struct{}

var _ apphost.Activator = unavailableActivator{}

func NewActivator() apphost.Activator { return unavailableActivator{} }

func (unavailableActivator) ActivateBundle(context.Context, string) error { return errUnavailable }
func (unavailableActivator) ActivatePID(context.Context, int) error       { return errUnavailable }
func (unavailableActivator) WaitFrontmostPID(context.Context, int) error  { return errUnavailable }
