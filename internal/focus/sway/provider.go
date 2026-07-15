package sway

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"waydict/internal/apperr"
	"waydict/internal/focus"
	"waydict/internal/swayipc"
)

type client interface {
	Available(context.Context) error
	Focused(context.Context) (swayipc.FocusedContainer, error)
}

type Provider struct {
	client client
	next   atomic.Uint64
	mu     sync.Mutex
	owned  map[uint64]swayipc.FocusedContainer
}

func New(client client) *Provider {
	return &Provider{client: client, owned: make(map[uint64]swayipc.FocusedContainer)}
}

func NewSocket(socket string) *Provider {
	return New(swayipc.New(socket))
}

func (p *Provider) Backend() string { return "sway" }

func (p *Provider) Available(ctx context.Context) error {
	if p == nil || p.client == nil {
		return apperr.New(apperr.CodeFocusUnavailable, "check Sway focus", fmt.Errorf("focus client is unavailable"))
	}
	if err := p.client.Available(ctx); err != nil {
		return apperr.New(apperr.CodeFocusUnavailable, "check Sway focus", err)
	}
	return nil
}

func (p *Provider) Current(ctx context.Context) (focus.Target, error) {
	if p == nil || p.client == nil {
		return focus.Target{}, apperr.New(apperr.CodeFocusUnavailable, "read Sway focus", fmt.Errorf("focus client is unavailable"))
	}
	container, err := p.client.Focused(ctx)
	if err != nil {
		return focus.Target{}, apperr.New(apperr.CodeFocusUnavailable, "read Sway focus", err)
	}
	token := p.next.Add(1)
	if token == 0 {
		token = p.next.Add(1)
	}
	p.mu.Lock()
	p.owned[token] = container
	p.mu.Unlock()
	appName := container.AppID
	if appName == "" {
		appName = container.Class
	}
	return focus.Target{
		Backend:  p.Backend(),
		StableID: fmt.Sprintf("sway:%d", container.ID),
		AppID:    container.AppID,
		AppName:  appName,
		PID:      container.PID,
		Token:    token,
	}, nil
}

func (p *Provider) Same(ctx context.Context, target focus.Target) (focus.Target, bool, error) {
	if p == nil {
		return focus.Target{}, false, apperr.New(apperr.CodeFocusUnavailable, "compare Sway focus", fmt.Errorf("focus provider is unavailable"))
	}
	p.mu.Lock()
	start, ok := p.owned[target.Token]
	p.mu.Unlock()
	if !ok {
		return focus.Target{}, false, apperr.New(apperr.CodeFocusUnavailable, "compare Sway focus", fmt.Errorf("focus target is no longer owned"))
	}
	current, err := p.Current(ctx)
	if err != nil {
		return focus.Target{}, false, err
	}
	p.mu.Lock()
	now := p.owned[current.Token]
	p.mu.Unlock()
	return current, now.ID == start.ID, nil
}

func (p *Provider) Release(target focus.Target) {
	if p == nil || target.Token == 0 {
		return
	}
	p.mu.Lock()
	delete(p.owned, target.Token)
	p.mu.Unlock()
}

func (p *Provider) Metadata(target focus.Target) focus.Metadata {
	if p == nil {
		return focus.Metadata{}
	}
	p.mu.Lock()
	container := p.owned[target.Token]
	p.mu.Unlock()
	return focus.Metadata{
		FocusedID:   container.ID,
		FocusedName: container.Name,
		Class:       container.Class,
		Workspace:   container.Workspace,
		Output:      container.Output,
	}
}

var _ focus.Provider = (*Provider)(nil)
var _ focus.MetadataProvider = (*Provider)(nil)
