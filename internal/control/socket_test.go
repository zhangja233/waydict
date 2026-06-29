package control

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"waydict/pkg/api"
)

type handlerFunc func(context.Context, Request) Response

func (f handlerFunc) HandleControl(ctx context.Context, req Request) Response {
	return f(ctx, req)
}

func TestServerSendRoundTrip(t *testing.T) {
	socket, stop := startTestServer(t, handlerFunc(func(_ context.Context, req Request) Response {
		return OK(req.ID, api.Status{State: api.StateIdle})
	}))
	defer stop()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req := NewRequest("status", nil)
	resp, err := Send(ctx, socket, req)
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK || resp.ID != req.ID || resp.Status.State != api.StateIdle {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestServerReturnsProtocolError(t *testing.T) {
	socket, stop := startTestServer(t, handlerFunc(func(_ context.Context, req Request) Response {
		return OK(req.ID, api.Status{State: api.StateIdle})
	}))
	defer stop()
	conn, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("{bad json}\n")); err != nil {
		t.Fatal(err)
	}
	var resp Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.OK || resp.Error == nil || resp.Error.Code != "protocol_error" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestPrepareSocketPathDoesNotChmodExistingDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "existing")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "waydict.sock")
	if err := prepareSocketPath(socket); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Mode().Perm(); got != 0755 {
		t.Fatalf("directory mode = %o, want 755", got)
	}
}

func TestPrepareSocketPathCreatesPrivateDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "new", "control")
	socket := filepath.Join(dir, "waydict.sock")
	if err := prepareSocketPath(socket); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Mode().Perm(); got != 0700 {
		t.Fatalf("directory mode = %o, want 700", got)
	}
}

func TestPrepareSocketRejectsNonSocketPath(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "waydict.sock")
	if err := os.WriteFile(socket, []byte("not a socket"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := prepareSocket(socket); err == nil {
		t.Fatal("expected non-socket path error")
	}
	if _, err := os.Stat(socket); err != nil {
		t.Fatalf("non-socket path was removed: %v", err)
	}
}

func TestPrepareSocketRejectsActiveSocket(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "waydict.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if err := prepareSocket(socket); err == nil {
		t.Fatal("expected active socket error")
	}
	if _, err := os.Stat(socket); err != nil {
		t.Fatalf("active socket path was removed: %v", err)
	}
}

func TestPrepareSocketRemovesStaleSocket(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "waydict.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	if err := prepareSocket(socket); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(socket); !os.IsNotExist(err) {
		t.Fatalf("stale socket still exists: %v", err)
	}
}

func startTestServer(t *testing.T, handler Handler) (string, func()) {
	t.Helper()
	socket := filepath.Join(t.TempDir(), "control", "waydict.sock")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- NewServer(socket, handler).Serve(ctx)
	}()
	deadline := time.Now().Add(time.Second)
	for {
		if _, err := os.Stat(socket); err == nil {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("server did not create socket")
		}
		time.Sleep(5 * time.Millisecond)
	}
	return socket, func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("server exited with error: %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("server did not stop")
		}
	}
}
