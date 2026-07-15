package loginitem

import "context"

type Service interface {
	Status(context.Context) (bool, error)
	SetEnabled(context.Context, bool) error
}
