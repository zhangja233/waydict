package sway

import (
	"context"
	"testing"

	"waydict/internal/swayipc"
)

type fakeClient struct {
	focused []swayipc.FocusedContainer
}

func (f *fakeClient) Available(context.Context) error { return nil }
func (f *fakeClient) Focused(context.Context) (swayipc.FocusedContainer, error) {
	current := f.focused[0]
	f.focused = f.focused[1:]
	return current, nil
}

func TestProviderIdentityTokensAndMetadata(t *testing.T) {
	client := &fakeClient{focused: []swayipc.FocusedContainer{
		{ID: 42, Name: "first title", AppID: "editor", PID: 7, Workspace: "1", Output: "DP-1"},
		{ID: 42, Name: "second title", AppID: "editor", PID: 7, Workspace: "2", Output: "DP-2"},
	}}
	provider := New(client)
	start, err := provider.Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if start.StableID != "sway:42" || start.Token == 0 {
		t.Fatalf("target = %#v", start)
	}
	current, same, err := provider.Same(context.Background(), start)
	if err != nil {
		t.Fatal(err)
	}
	if !same || current.Token <= start.Token {
		t.Fatalf("current = %#v, same = %v", current, same)
	}
	metadata := provider.Metadata(current)
	if metadata.FocusedName != "second title" || metadata.Workspace != "2" {
		t.Fatalf("metadata = %#v", metadata)
	}
	provider.Release(start)
	provider.Release(start)
	provider.Release(current)
}
