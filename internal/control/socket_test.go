package control

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
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

func TestPrepareSocketPathEnforcesPrivateExistingDirectory(t *testing.T) {
	dir := filepath.Join(shortTempDir(t), "existing")
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
	if got := st.Mode().Perm(); got != 0700 {
		t.Fatalf("directory mode = %o, want 700", got)
	}
}

func TestPrepareSocketPathCreatesPrivateDirectory(t *testing.T) {
	dir := filepath.Join(shortTempDir(t), "new", "control")
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

func TestPrepareSocketPathRejectsSymlinkDirectory(t *testing.T) {
	base := shortTempDir(t)
	realDir := filepath.Join(base, "real")
	if err := os.Mkdir(realDir, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(realDir, link); err != nil {
		t.Fatal(err)
	}
	if err := prepareSocketPath(filepath.Join(link, "control.sock")); err == nil {
		t.Fatal("expected symlink directory rejection")
	}
}

func TestPrepareSocketRejectsNonSocketPath(t *testing.T) {
	socket := filepath.Join(shortTempDir(t), "waydict.sock")
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
	socket := filepath.Join(shortTempDir(t), "waydict.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if err := prepareSocket(socket); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatal("expected active socket error")
	}
	if _, err := os.Stat(socket); err != nil {
		t.Fatalf("active socket path was removed: %v", err)
	}
}

func TestPrepareSocketRemovesStaleSocket(t *testing.T) {
	socket := filepath.Join(shortTempDir(t), "waydict.sock")
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
	socket := filepath.Join(shortTempDir(t), "control", "waydict.sock")
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

func TestReadFrameBoundsAndUTF8(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		ok   bool
	}{
		{name: "valid", data: []byte("{}\n"), ok: true},
		{name: "unterminated", data: []byte("{}")},
		{name: "invalid UTF-8", data: []byte{'{', 0xff, '}', '\n'}},
		{name: "too large", data: append(bytes.Repeat([]byte{'x'}, MaxControlFrameBytes+1), '\n')},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := readFrame(bufio.NewReader(bytes.NewReader(tc.data)))
			if (err == nil) != tc.ok {
				t.Fatalf("readFrame() error = %v, ok=%t", err, tc.ok)
			}
		})
	}
}

func TestValidateSocketPathLength(t *testing.T) {
	if err := ValidateSocketPathFor("darwin", "/tmp/waydict-501/control.sock"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateSocketPathFor("darwin", "/tmp/"+strings.Repeat("x", 100)); err == nil {
		t.Fatal("expected Darwin path-length error")
	}
}

func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "waydict-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}
