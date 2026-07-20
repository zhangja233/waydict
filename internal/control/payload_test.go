package control

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"waydict/pkg/api"
)

func TestSendWithPayloadRoundTrip(t *testing.T) {
	payload := bytes.Repeat([]byte{0xAB, 0xCD}, 4096)
	var got []byte
	var declared any
	socket, stop := startTestServer(t, handlerFunc(func(_ context.Context, req Request) Response {
		got = append([]byte(nil), req.Payload...)
		declared = req.Args["payload_bytes"]
		return OK(req.ID, api.Status{State: api.StateIdle})
	}))
	defer stop()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req := NewRequest("transcribe", map[string]any{"codec": "pcm_s16le"})
	resp, err := SendWithPayload(ctx, socket, req, payload, 200*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload round-trip mismatch: got %d bytes, want %d", len(got), len(payload))
	}
	if declared != float64(len(payload)) {
		t.Fatalf("payload_bytes = %v, want %d", declared, len(payload))
	}
}

// The caller declares the payload length by handing over bytes, never by
// setting payload_bytes itself, so a stale arg cannot desync the frame.
func TestSendWithPayloadOverridesStaleDeclaredLength(t *testing.T) {
	payload := []byte("actual body")
	var got []byte
	socket, stop := startTestServer(t, handlerFunc(func(_ context.Context, req Request) Response {
		got = append([]byte(nil), req.Payload...)
		return OK(req.ID, api.Status{State: api.StateIdle})
	}))
	defer stop()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req := NewRequest("transcribe", map[string]any{"payload_bytes": 99999})
	if _, err := SendWithPayload(ctx, socket, req, payload, 0); err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Fatalf("payload = %q, want %q", got, payload)
	}
}

// Every pre-existing command sends no payload and must stay wire-compatible.
func TestSendWithoutPayloadLeavesArgsAlone(t *testing.T) {
	var args map[string]any
	var payloadLen int
	socket, stop := startTestServer(t, handlerFunc(func(_ context.Context, req Request) Response {
		args = req.Args
		payloadLen = len(req.Payload)
		return OK(req.ID, api.Status{State: api.StateIdle})
	}))
	defer stop()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := Send(ctx, socket, NewRequest("status", map[string]any{"mode": "hold"})); err != nil {
		t.Fatal(err)
	}
	if payloadLen != 0 {
		t.Fatalf("payload length = %d, want 0", payloadLen)
	}
	if _, ok := args["payload_bytes"]; ok {
		t.Fatalf("payload_bytes leaked into a payload-free request: %+v", args)
	}
}

func TestSendWithPayloadRejectsOversizedBody(t *testing.T) {
	socket, stop := startTestServer(t, handlerFunc(func(_ context.Context, req Request) Response {
		return OK(req.ID, api.Status{State: api.StateIdle})
	}))
	defer stop()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := SendWithPayload(ctx, socket, NewRequest("transcribe", nil), make([]byte, MaxPayloadBytes+1), 0)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("error = %v, want an oversize rejection", err)
	}
}

// A peer that declares more than it sends must be rejected rather than leaving
// the server blocked on a body that never arrives.
func TestServerRejectsTruncatedPayload(t *testing.T) {
	handled := false
	socket, stop := startTestServer(t, handlerFunc(func(_ context.Context, req Request) Response {
		handled = true
		return OK(req.ID, api.Status{State: api.StateIdle})
	}))
	defer stop()
	resp := rawExchange(t, socket, "{\"version\":1,\"id\":\"x\",\"command\":\"transcribe\",\"args\":{\"payload_bytes\":64}}\n", []byte("short"))
	if resp.OK || resp.Error == nil || resp.Error.Code != "protocol_error" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if handled {
		t.Fatal("handler ran on a truncated frame")
	}
}

// rawExchange writes a hand-built frame so tests can send bodies the client
// would refuse to build. It half-closes so a short body reaches the server as
// EOF instead of hanging it.
func rawExchange(t *testing.T, socket, frame string, body []byte) Response {
	t.Helper()
	conn, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte(frame)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := conn.(*net.UnixConn).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	var resp Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestPayloadBytesValidation(t *testing.T) {
	tests := []struct {
		name string
		args map[string]any
		want int
		ok   bool
	}{
		{name: "absent", args: map[string]any{}, want: 0, ok: true},
		{name: "integer", args: map[string]any{"payload_bytes": float64(32)}, want: 32, ok: true},
		{name: "fractional", args: map[string]any{"payload_bytes": 1.5}},
		{name: "negative", args: map[string]any{"payload_bytes": float64(-1)}},
		{name: "oversize", args: map[string]any{"payload_bytes": float64(MaxPayloadBytes + 1)}},
		{name: "wrong type", args: map[string]any{"payload_bytes": "32"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := PayloadBytes(tc.args)
			if (err == nil) != tc.ok {
				t.Fatalf("PayloadBytes() error = %v, ok = %t", err, tc.ok)
			}
			if tc.ok && got != tc.want {
				t.Fatalf("PayloadBytes() = %d, want %d", got, tc.want)
			}
		})
	}
}
