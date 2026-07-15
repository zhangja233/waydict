//go:build !darwin || !cgo

package loginitem

import (
	"context"
	"errors"

	loginitemmodel "waydict/internal/loginitem"
)

var errUnavailable = errors.New("macOS login-item service requires Darwin with cgo")

type service struct{}

var _ loginitemmodel.Service = (*service)(nil)

func New() loginitemmodel.Service {
	return &service{}
}

func (s *service) Status(ctx context.Context) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return false, errUnavailable
}

func (s *service) SetEnabled(ctx context.Context, enabled bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return errUnavailable
}
