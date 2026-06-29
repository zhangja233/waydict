package swayipc

import (
	"context"
	"fmt"
)

type Policy string

const (
	CancelOnFocusChange Policy = "cancel_on_focus_change"
	WarnAndType         Policy = "warn_and_type"
	TypeCurrent         Policy = "type_current"
)

type Guard struct {
	client *Client
	policy Policy
	start  *FocusedContainer
}

func NewGuard(client *Client, policy string) *Guard {
	return &Guard{client: client, policy: Policy(policy)}
}

func (g *Guard) CaptureStart(ctx context.Context) error {
	if g.policy == TypeCurrent || g.client == nil {
		g.start = nil
		return nil
	}
	f, err := g.client.Focused(ctx)
	if err != nil {
		return err
	}
	g.start = &f
	return nil
}

func (g *Guard) Check(ctx context.Context) error {
	if g.policy == TypeCurrent || g.client == nil || g.start == nil {
		return nil
	}
	f, err := g.client.Focused(ctx)
	if err != nil {
		return err
	}
	if f.ID != g.start.ID && g.policy == CancelOnFocusChange {
		return fmt.Errorf("focus_changed: focus changed from %d to %d", g.start.ID, f.ID)
	}
	return nil
}

func (g *Guard) StartedFocus() *FocusedContainer {
	if g.start == nil {
		return nil
	}
	f := *g.start
	return &f
}
