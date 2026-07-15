package control

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"waydict/pkg/api"
)

var update = flag.Bool("update", false, "update golden files")

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

func TestRequestJSONGolden(t *testing.T) {
	tests := []struct {
		name string
		req  Request
	}{
		{name: "start", req: Request{Version: Version, ID: "request-start", Command: "start", Args: map[string]any{"mode": "hold"}}},
		{name: "stop", req: Request{Version: Version, ID: "request-stop", Command: "stop", Args: map[string]any{"commit": true, "discard": false}}},
		{name: "release", req: Request{Version: Version, ID: "request-release", Command: "release", Args: map[string]any{}}},
		{name: "toggle", req: Request{Version: Version, ID: "request-toggle", Command: "toggle", Args: map[string]any{}}},
		{name: "status", req: Request{Version: Version, ID: "request-status", Command: "status", Args: map[string]any{}}},
		{name: "reload_config", req: Request{Version: Version, ID: "request-reload", Command: "reload_config", Args: nil}},
		{name: "shutdown", req: Request{Version: Version, ID: "request-shutdown", Command: "shutdown", Args: nil}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertProtocolJSONGolden(t, filepath.Join("testdata", "request_"+tt.name+".golden.json"), tt.req)
		})
	}
}

func TestResponseJSONGolden(t *testing.T) {
	status := api.Status{State: api.StateListening, LastTranscriptRedacted: true}
	tests := []struct {
		name string
		resp Response
	}{
		{name: "ok", resp: OK("request-ok", status)},
		{name: "error", resp: Fail("request-error", "focus_changed", "focused target changed", status)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertProtocolJSONGolden(t, filepath.Join("testdata", "response_"+tt.name+".golden.json"), tt.resp)
		})
	}
}

func assertProtocolJSONGolden(t *testing.T, path string, value any) {
	t.Helper()
	got, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	if *update {
		if err := os.WriteFile(path, got, 0644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("JSON does not match %s; run go test -update to refresh\n\ngot:\n%s\nwant:\n%s", path, got, want)
	}
}
