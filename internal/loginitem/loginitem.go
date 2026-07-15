package loginitem

import "context"

type Service interface {
	Enabled(context.Context) (bool, error)
	SetEnabled(context.Context, bool) error
}
