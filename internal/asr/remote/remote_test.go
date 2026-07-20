package remote

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"waydict/internal/asr"
	"waydict/internal/control"
	"waydict/pkg/api"
)

func TestTranscribeUsesPeerWhenReachable(t *testing.T) {
	var seen asr.AudioSegment
	socket := startPeer(t, func(req control.Request) control.Response {
		segment, err := asr.DecodeSegment(req.Args, req.Payload)
		if err != nil {
			return control.Fail(req.ID, "usage", err.Error(), api.Status{})
		}
		seen = segment
		return control.OKData(req.ID, api.Status{}, asr.EncodeTranscript(asr.Transcript{
			SegmentID:      segment.ID,
			Text:           "decoded on the peer",
			DecodeDuration: 40 * time.Millisecond,
			RealTimeFactor: 0.02,
		}))
	})
	fallback := &recordingEngine{text: "decoded locally"}
	engine := New(Options{Socket: socket}, fallback)
	transcript, err := engine.Transcribe(context.Background(), testSegment())
	if err != nil {
		t.Fatal(err)
	}
	if transcript.Text != "decoded on the peer" {
		t.Fatalf("text = %q, want the peer's", transcript.Text)
	}
	if fallback.calls != 0 {
		t.Fatalf("fallback ran %d times while the peer was reachable", fallback.calls)
	}
	if len(seen.Samples) != len(testSegment().Samples) || seen.SampleRate != 16000 {
		t.Fatalf("peer received %d samples at %d Hz", len(seen.Samples), seen.SampleRate)
	}
	if status := engine.RemoteStatus(); status.Served != asr.ServedRemote || status.LastError != "" {
		t.Fatalf("status = %+v, want a clean remote decode", status)
	}
}

// The roaming case: nothing is listening, so the dial fails outright.
func TestTranscribeFallsBackWhenPeerIsUnreachable(t *testing.T) {
	fallback := &recordingEngine{text: "decoded locally"}
	engine := New(Options{Socket: unusedSocketPath(t)}, fallback)
	transcript, err := engine.Transcribe(context.Background(), testSegment())
	if err != nil {
		t.Fatal(err)
	}
	if transcript.Text != "decoded locally" {
		t.Fatalf("text = %q, want the fallback's", transcript.Text)
	}
	status := engine.RemoteStatus()
	if status.Served != asr.ServedFallback {
		t.Fatalf("served = %q, want %q", status.Served, asr.ServedFallback)
	}
	if status.LastError == "" {
		t.Fatal("fallback recorded no reason for skipping the peer")
	}
}

// A peer that answers but refuses — serve_remote_asr off, unknown codec, engine
// unloaded — must fall back just like an unreachable one.
func TestTranscribeFallsBackWhenPeerRefuses(t *testing.T) {
	socket := startPeer(t, func(req control.Request) control.Response {
		return control.Fail(req.ID, "not_permitted", "daemon.serve_remote_asr is disabled", api.Status{})
	})
	fallback := &recordingEngine{text: "decoded locally"}
	engine := New(Options{Socket: socket}, fallback)
	transcript, err := engine.Transcribe(context.Background(), testSegment())
	if err != nil {
		t.Fatal(err)
	}
	if transcript.Text != "decoded locally" {
		t.Fatalf("text = %q, want the fallback's", transcript.Text)
	}
	if status := engine.RemoteStatus(); !strings.Contains(status.LastError, "not_permitted") {
		t.Fatalf("last error = %q, want the peer's refusal", status.LastError)
	}
}

func TestTranscribeWithoutFallbackReturnsTheRemoteError(t *testing.T) {
	engine := New(Options{Socket: unusedSocketPath(t)}, nil)
	if _, err := engine.Transcribe(context.Background(), testSegment()); err == nil {
		t.Fatal("expected an error when remote decoding is mandatory")
	}
	if status := engine.RemoteStatus(); status.Served != asr.ServedRemote || status.Fallback != "" {
		t.Fatalf("status = %+v, want a remote-only engine", status)
	}
}

func TestTranscribeReportsFallbackFailure(t *testing.T) {
	fallback := &recordingEngine{err: errors.New("model missing")}
	engine := New(Options{Socket: unusedSocketPath(t)}, fallback)
	_, err := engine.Transcribe(context.Background(), testSegment())
	if err == nil || !strings.Contains(err.Error(), "model missing") {
		t.Fatalf("error = %v, want the fallback's failure", err)
	}
}

// The remote attempt must leave the fallback enough of the caller's deadline to
// finish locally, or an offline laptop would time out instead of degrading.
func TestRemoteBudgetReservesHalfForTheFallback(t *testing.T) {
	engine := New(Options{Socket: "/tmp/x.sock", RequestTimeout: time.Minute}, &recordingEngine{})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if budget := engine.remoteBudget(ctx); budget > 5*time.Second || budget < 4*time.Second {
		t.Fatalf("budget = %v, want about half of the 10s deadline", budget)
	}
}

// With nothing to fall back to there is no reason to hold time in reserve.
func TestRemoteBudgetSpendsEverythingWithoutAFallback(t *testing.T) {
	engine := New(Options{Socket: "/tmp/x.sock", RequestTimeout: time.Second}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if budget := engine.remoteBudget(ctx); budget < 9*time.Second {
		t.Fatalf("budget = %v, want the full deadline", budget)
	}
}

// A roaming laptop must still start when the peer is gone.
func TestLoadSucceedsWithAnUnreachablePeer(t *testing.T) {
	engine := New(Options{Socket: unusedSocketPath(t)}, &recordingEngine{})
	if err := engine.Load(context.Background()); err != nil {
		t.Fatalf("Load() = %v, want nil for an unreachable peer", err)
	}
	if !engine.Loaded() {
		t.Fatal("Loaded() = false")
	}
}

func TestReachableProbesThePeer(t *testing.T) {
	socket := startPeer(t, func(req control.Request) control.Response {
		if req.Command != "status" {
			return control.Fail(req.ID, "usage", "unexpected command", api.Status{})
		}
		return control.OK(req.ID, api.Status{State: api.StateIdle})
	})
	engine := New(Options{Socket: socket}, nil)
	if err := engine.Reachable(context.Background()); err != nil {
		t.Fatalf("Reachable() = %v", err)
	}
	unreachable := New(Options{Socket: unusedSocketPath(t)}, nil)
	if err := unreachable.Reachable(context.Background()); err == nil {
		t.Fatal("Reachable() = nil for a socket with no listener")
	}
}

func testSegment() asr.AudioSegment {
	return asr.AudioSegment{
		ID:         "seg-1",
		Samples:    make([]float32, 16000),
		SampleRate: 16000,
		Duration:   time.Second,
	}
}

// startPeer stands in for another host's daemon behind the SSH forward.
func startPeer(t *testing.T, handle func(control.Request) control.Response) string {
	t.Helper()
	socket := unusedSocketPath(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- control.NewServer(socket, handlerFunc(handle)).Serve(ctx)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(socket); err == nil {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("peer did not create its socket")
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("peer did not stop")
		}
	})
	return socket
}

// unusedSocketPath keeps paths well under the 107-byte sun_path limit, which
// the default test temp dir can exceed.
func unusedSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "wdr")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "peer.sock")
}

type handlerFunc func(control.Request) control.Response

func (f handlerFunc) HandleControl(_ context.Context, req control.Request) control.Response {
	return f(req)
}

type recordingEngine struct {
	mu    sync.Mutex
	calls int
	text  string
	err   error
}

func (e *recordingEngine) Name() string { return "sherpa-onnx" }
func (e *recordingEngine) Loaded() bool { return true }
func (e *recordingEngine) Close() error { return nil }
func (e *recordingEngine) Load(context.Context) error {
	return nil
}

func (e *recordingEngine) Transcribe(_ context.Context, segment asr.AudioSegment) (asr.Transcript, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls++
	if e.err != nil {
		return asr.Transcript{}, fmt.Errorf("fallback: %w", e.err)
	}
	return asr.Transcript{SegmentID: segment.ID, Text: e.text}, nil
}
