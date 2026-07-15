//go:build !darwin || !cgo

package macos

import (
	"context"
	"fmt"
	"sync"

	"waydict/internal/apperr"
	"waydict/internal/hotkey"
)

const Supported = false

type Service struct {
	mu     sync.Mutex
	status hotkey.Status
}

func New() *Service {
	return &Service{}
}

func (s *Service) Available(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return unavailable("check global hotkey")
}

func (s *Service) Start(ctx context.Context, binding hotkey.Binding, _ hotkey.Handler) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	s.status = hotkey.Status{Binding: binding, LastErrorCode: apperr.CodeHotkeyUnavailable}
	s.mu.Unlock()
	return unavailable("start global hotkey")
}

func (s *Service) Rebind(ctx context.Context, binding hotkey.Binding) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	s.status.Binding = binding
	s.status.LastErrorCode = apperr.CodeHotkeyUnavailable
	s.mu.Unlock()
	return unavailable("rebind global hotkey")
}

func (s *Service) Stop(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	s.status.Running = false
	s.mu.Unlock()
	return nil
}

func (s *Service) Status() hotkey.Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

func unavailable(operation string) error {
	return apperr.New(apperr.CodeHotkeyUnavailable, operation, fmt.Errorf("macOS event taps require Darwin with cgo"))
}

var _ hotkey.Service = (*Service)(nil)
