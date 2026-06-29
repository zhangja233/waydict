package control

import (
	"encoding/json"
	"testing"

	"waydict/pkg/api"
)

func TestProtocolRoundTrip(t *testing.T) {
	req := NewRequest("toggle", map[string]any{"x": true})
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var got Request
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Version != Version || got.Command != "toggle" || got.ID == "" {
		t.Fatalf("bad request round trip: %+v", got)
	}
	resp := OK(got.ID, api.Status{State: api.StateListening})
	if !resp.OK || resp.ID != got.ID || resp.Status.State != api.StateListening {
		t.Fatalf("bad response: %+v", resp)
	}
}
