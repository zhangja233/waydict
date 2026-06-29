package inject

import "context"

type Injector interface {
	TypeText(ctx context.Context, text string) error
	Available(ctx context.Context) error
}
