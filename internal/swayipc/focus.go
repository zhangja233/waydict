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

type FocusChange struct {
	From FocusedContainer
	To   FocusedContainer
}

func (c *FocusChange) Error() string {
	return fmt.Sprintf("focus_changed: focus changed from %d to %d", c.From.ID, c.To.ID)
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
	_, err := g.CheckWithWarning(ctx)
	return err
}

func (g *Guard) CheckWithWarning(ctx context.Context) (*FocusChange, error) {
	if g.policy == TypeCurrent || g.client == nil || g.start == nil {
		return nil, nil
	}
	f, err := g.client.Focused(ctx)
	if err != nil {
		return nil, err
	}
	if f.ID == g.start.ID {
		return nil, nil
	}
	change := &FocusChange{From: *g.start, To: f}
	if g.policy == CancelOnFocusChange {
		return nil, change
	}
	if g.policy == WarnAndType {
		return change, nil
	}
	return nil, nil
}

func (g *Guard) StartedFocus() *FocusedContainer {
	if g.start == nil {
		return nil
	}
	f := *g.start
	return &f
}
