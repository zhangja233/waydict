package apphost

import "context"

type Activator interface {
	ActivateBundle(context.Context, string) error
	ActivatePID(context.Context, int) error
	WaitFrontmostPID(context.Context, int) error
}
