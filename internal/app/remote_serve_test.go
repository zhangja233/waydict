package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"waydict/internal/asr"
	remoteasr "waydict/internal/asr/remote"
	"waydict/internal/config"
	"waydict/internal/control"
	svlog "waydict/internal/log"
)

func TestServeRemoteTranscribeRejectedWhenDisabled(t *testing.T) {
	application := newServingApp(t, false, nil, &bytes.Buffer{})
	response := application.HandleControl(context.Background(), remoteRequest(t))
	if response.OK {
		t.Fatal("transcribe was served with daemon.serve_remote_asr disabled")
	}
	if response.Error == nil || response.Error.Code != "not_permitted" {
		t.Fatalf("error = %+v, want not_permitted", response.Error)
	}
}

func TestServeRemoteTranscribeDecodesForThePeer(t *testing.T) {
	engine := &peerEngine{FakeEngine: FakeEngine{Text: "hello from the gpu", IsLoaded: true}}
	application := newServingApp(t, true, engine, &bytes.Buffer{})
	response := application.HandleControl(context.Background(), remoteRequest(t))
	if !response.OK {
		t.Fatalf("transcribe failed: %+v", response.Error)
	}
	transcript, err := asr.DecodeTranscript(response.Data, asr.AudioSegment{})
	if err != nil {
		t.Fatal(err)
	}
	if transcript.Text != "hello from the gpu" {
		t.Fatalf("text = %q, want the engine's output", transcript.Text)
	}
	if engine.segment.SampleRate != 16000 || len(engine.segment.Samples) != 16000 {
		t.Fatalf("engine saw %d samples at %d Hz", len(engine.segment.Samples), engine.segment.SampleRate)
	}
	if engine.segment.Duration != time.Second {
		t.Fatalf("duration = %v, want 1s", engine.segment.Duration)
	}
}

// Serving a peer must not leak what this host is doing: not its own last
// transcript, not the window its user is looking at. Only the decoded text the
// peer asked for may cross, and it must never reach this host's log.
func TestServeRemoteTranscribeKeepsHostContextPrivate(t *testing.T) {
	const hostSecret = "HOST-TRANSCRIPT-MUST-NOT-APPEAR"
	const peerText = "the peer's own words"
	var output bytes.Buffer
	application := newServingApp(t, true, &peerEngine{FakeEngine: FakeEngine{Text: peerText, IsLoaded: true}}, &output)
	application.recordTranscript(hostSecret)
	application.mu.Lock()
	application.status.Focus.AppName = hostSecret
	application.status.Focus.FocusedName = hostSecret
	application.mu.Unlock()

	response := application.HandleControl(context.Background(), remoteRequest(t))
	if !response.OK {
		t.Fatalf("transcribe failed: %+v", response.Error)
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), hostSecret) {
		t.Fatalf("host context reached the peer: %s", encoded)
	}
	if response.Status.LastTranscript != "" || !response.Status.LastTranscriptRedacted {
		t.Fatalf("peer-visible status was not redacted: %+v", response.Status)
	}
	if strings.Contains(output.String(), peerText) {
		t.Fatalf("the peer's transcript reached this host's log: %s", output.String())
	}
}

func TestServeRemoteTranscribeRejectsUnknownCodec(t *testing.T) {
	application := newServingApp(t, true, &peerEngine{FakeEngine: FakeEngine{IsLoaded: true}}, &bytes.Buffer{})
	req := remoteRequest(t)
	req.Args["codec"] = "opus"
	response := application.HandleControl(context.Background(), req)
	if response.OK {
		t.Fatal("an unknown codec was accepted")
	}
	if response.Error == nil || response.Error.Code != "usage" {
		t.Fatalf("error = %+v, want usage", response.Error)
	}
}

// The halves are unit-tested apart; this drives the real client through a real
// Unix socket into a real serving daemon, which is what the SSH forward carries.
func TestRemoteEngineDecodesThroughARealServingDaemon(t *testing.T) {
	engine := &peerEngine{FakeEngine: FakeEngine{Text: "over the wire", IsLoaded: true}}
	socket := serveOnSocket(t, newServingApp(t, true, engine, &bytes.Buffer{}))

	client := remoteasr.New(remoteasr.Options{Socket: socket}, nil)
	segment := asr.AudioSegment{ID: "seg-1", Samples: make([]float32, 16000), SampleRate: 16000, Duration: time.Second}
	segment.Samples[100] = 0.5
	transcript, err := client.Transcribe(context.Background(), segment)
	if err != nil {
		t.Fatal(err)
	}
	if transcript.Text != "over the wire" {
		t.Fatalf("text = %q, want the peer's", transcript.Text)
	}
	if got := engine.segment.Samples[100]; got < 0.49 || got > 0.51 {
		t.Fatalf("sample survived the wire as %v, want ~0.5", got)
	}
	if status := client.RemoteStatus(); status.Served != asr.ServedRemote {
		t.Fatalf("served = %q, want %q", status.Served, asr.ServedRemote)
	}
}

// A client pointed at a daemon that has not opted in must fall back rather than
// fail — that is what happens before the serving host is redeployed.
func TestRemoteEngineFallsBackWhenTheDaemonHasNotOptedIn(t *testing.T) {
	socket := serveOnSocket(t, newServingApp(t, false, &peerEngine{FakeEngine: FakeEngine{IsLoaded: true}}, &bytes.Buffer{}))
	fallback := &peerEngine{FakeEngine: FakeEngine{Text: "decoded locally", IsLoaded: true}}

	client := remoteasr.New(remoteasr.Options{Socket: socket}, fallback)
	transcript, err := client.Transcribe(context.Background(), asr.AudioSegment{
		ID: "seg-1", Samples: make([]float32, 16000), SampleRate: 16000, Duration: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if transcript.Text != "decoded locally" {
		t.Fatalf("text = %q, want the fallback's", transcript.Text)
	}
	if status := client.RemoteStatus(); !strings.Contains(status.LastError, "not_permitted") {
		t.Fatalf("last error = %q, want the daemon's refusal", status.LastError)
	}
}

// serveOnSocket runs application's control server on a short-enough path (the
// default test temp dir can exceed the 107-byte sun_path limit).
func serveOnSocket(t *testing.T, application *App) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "wda")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "peer.sock")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- control.NewServer(socket, application).Serve(ctx) }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(socket); err == nil {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("control server did not create its socket")
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("control server did not stop")
		}
	})
	return socket
}

func newServingApp(t *testing.T, serve bool, engine asr.Engine, output *bytes.Buffer) *App {
	t.Helper()
	cfg := config.DefaultsFor("linux", config.PlatformPaths{})
	cfg.Daemon.ServeRemoteASR = serve
	cfg.Daemon.RedactTranscriptsInLogs = false
	cfg.Focus.Enabled = false
	return New(context.Background(), cfg, Dependencies{
		Capabilities: ControlCapabilities{Platform: "linux"},
		Logger:       svlog.New("debug", output),
		Engine:       engine,
	})
}

func remoteRequest(t *testing.T) control.Request {
	t.Helper()
	segment := asr.AudioSegment{
		ID:         "seg-1",
		Samples:    make([]float32, 16000),
		SampleRate: 16000,
		Duration:   time.Second,
	}
	// Args arrive through JSON, so numbers reach the handler as float64.
	var args map[string]any
	encoded, err := json.Marshal(asr.EncodeSegmentArgs(segment, asr.CodecPCMS16LE))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &args); err != nil {
		t.Fatal(err)
	}
	req := control.NewRequest("transcribe", args)
	req.Payload = asr.EncodePCMS16LE(segment.Samples)
	return req
}

// peerEngine keeps the segment a borrowing client sent, so tests can assert the
// audio survived the wire intact.
type peerEngine struct {
	FakeEngine
	segment asr.AudioSegment
}

func (e *peerEngine) Transcribe(ctx context.Context, segment asr.AudioSegment) (asr.Transcript, error) {
	e.segment = segment
	return e.FakeEngine.Transcribe(ctx, segment)
}
