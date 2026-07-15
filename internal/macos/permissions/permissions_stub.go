//go:build !darwin || !cgo

package permissions

import (
	"context"
	"errors"
	"time"

	permissionmodel "waydict/internal/permissions"
)

var errUnavailable = errors.New("macOS permission service requires Darwin with cgo")

type source struct{}

var _ permissionmodel.Source = (*source)(nil)

func New() permissionmodel.Source {
	return &source{}
}

func (s *source) Snapshot(ctx context.Context) (permissionmodel.Snapshot, error) {
	snapshot := permissionmodel.UnavailableSnapshot(time.Now())
	if err := ctx.Err(); err != nil {
		return snapshot, err
	}
	return snapshot, nil
}

func (s *source) Request(ctx context.Context, kind permissionmodel.Kind) (permissionmodel.State, error) {
	if err := ctx.Err(); err != nil {
		return permissionmodel.Unavailable, err
	}
	return permissionmodel.Unavailable, errUnavailable
}

func (s *source) OpenSettings(ctx context.Context, kind permissionmodel.Kind) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return errUnavailable
}
