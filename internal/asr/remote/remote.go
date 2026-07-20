// Package remote decodes segments on another host's waydict daemon, reached
// through a Unix socket that something outside waydict (an SSH -L forward)
// points at the peer. Keeping the transport a Unix socket is deliberate: it
// leaves authentication, encryption, and reachability to SSH, and keeps this
// package inside the repo's no-outbound-network policy.
package remote

import (
	"context"
	"fmt"
	"sync"
	"time"

	"waydict/internal/asr"
	"waydict/internal/config"
	"waydict/internal/control"
)

// Options configures the client half of remote decoding.
type Options struct {
	Socket         string
	Codec          string
	DialTimeout    time.Duration
	RequestTimeout time.Duration
	// PreloadFallback loads the local engine at startup instead of on the first
	// failure. Off by default: holding a second model resident defeats the point
	// of offloading, and the lazy load costs a second only once.
	PreloadFallback bool
}

// Engine implements asr.Engine by forwarding to a peer, with an optional local
// engine covering every way the peer can be unavailable.
type Engine struct {
	opts     Options
	fallback asr.Engine

	mu     sync.Mutex
	status asr.RemoteStatus
}

// OptionsFromConfig maps the [asr.remote] table onto Options, so the daemon,
// the CLI, and doctor all build the client the same way.
func OptionsFromConfig(cfg config.Config) Options {
	return Options{
		Socket:          cfg.ASR.Remote.Socket,
		Codec:           cfg.ASR.Remote.Codec,
		DialTimeout:     time.Duration(cfg.ASR.Remote.DialTimeoutMS) * time.Millisecond,
		RequestTimeout:  time.Duration(cfg.ASR.Remote.RequestTimeoutMS) * time.Millisecond,
		PreloadFallback: cfg.Daemon.PreloadModel,
	}
}

// New builds a remote engine. A nil fallback makes remote decoding mandatory:
// segments fail rather than degrading to local CPU.
func New(opts Options, fallback asr.Engine) *Engine {
	if opts.Codec == "" {
		opts.Codec = asr.CodecPCMS16LE
	}
	if opts.DialTimeout <= 0 {
		opts.DialTimeout = 300 * time.Millisecond
	}
	if opts.RequestTimeout <= 0 {
		opts.RequestTimeout = 8 * time.Second
	}
	engine := &Engine{opts: opts, fallback: fallback}
	engine.status.Socket = opts.Socket
	if fallback != nil {
		engine.status.Fallback = fallback.Name()
	}
	return engine
}

func (e *Engine) Name() string { return "remote" }

// Loaded reports true because there is nothing local to load. The fallback, if
// any, loads itself on first use.
func (e *Engine) Loaded() bool { return true }

// Load never fails on an unreachable peer — a roaming laptop must still start,
// and reachability is decided per segment anyway.
func (e *Engine) Load(ctx context.Context) error {
	if e.opts.PreloadFallback && e.fallback != nil {
		return e.fallback.Load(ctx)
	}
	return nil
}

func (e *Engine) Close() error {
	if e.fallback != nil {
		return e.fallback.Close()
	}
	return nil
}

// RemoteStatus exposes which side served the last segment.
func (e *Engine) RemoteStatus() asr.RemoteStatus {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.status
}

// Reachable probes the peer with a plain status request. Used by doctor; the
// decode path does not pre-probe, because a failed dial is already the answer.
func (e *Engine) Reachable(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, e.opts.RequestTimeout)
	defer cancel()
	resp, err := control.SendWithPayload(ctx, e.opts.Socket, control.NewRequest("status", nil), nil, e.opts.DialTimeout)
	if err != nil {
		return err
	}
	if !resp.OK {
		return responseError(resp)
	}
	return nil
}

func (e *Engine) Transcribe(ctx context.Context, segment asr.AudioSegment) (asr.Transcript, error) {
	transcript, err := e.transcribeRemote(ctx, segment)
	if err == nil {
		e.record(asr.ServedRemote, "", transcript.RealTimeFactor)
		return transcript, nil
	}
	if e.fallback == nil {
		e.record(asr.ServedRemote, err.Error(), 0)
		return asr.Transcript{}, fmt.Errorf("remote asr at %s: %w", e.opts.Socket, err)
	}
	transcript, fallbackErr := e.fallback.Transcribe(ctx, segment)
	if fallbackErr != nil {
		e.record(asr.ServedFallback, err.Error(), 0)
		return asr.Transcript{}, fallbackErr
	}
	e.record(asr.ServedFallback, err.Error(), transcript.RealTimeFactor)
	return transcript, nil
}

func (e *Engine) transcribeRemote(ctx context.Context, segment asr.AudioSegment) (asr.Transcript, error) {
	if segment.SampleRate <= 0 {
		return asr.Transcript{}, fmt.Errorf("invalid segment sample rate %d", segment.SampleRate)
	}
	if e.opts.Codec != asr.CodecPCMS16LE {
		return asr.Transcript{}, fmt.Errorf("unsupported codec %q", e.opts.Codec)
	}
	ctx, cancel := context.WithTimeout(ctx, e.remoteBudget(ctx))
	defer cancel()
	req := control.NewRequest(asr.RemoteCommand, asr.EncodeSegmentArgs(segment, e.opts.Codec))
	payload := asr.EncodePCMS16LE(segment.Samples)
	resp, err := control.SendWithPayload(ctx, e.opts.Socket, req, payload, e.opts.DialTimeout)
	if err != nil {
		return asr.Transcript{}, err
	}
	if !resp.OK {
		return asr.Transcript{}, responseError(resp)
	}
	return asr.DecodeTranscript(resp.Data, segment)
}

// remoteBudget spends at most half of the caller's remaining deadline on the
// peer, so a stalled link still leaves the fallback enough time to decode
// locally within the same overall budget. It scales with segment length because
// the caller's deadline does.
func (e *Engine) remoteBudget(ctx context.Context) time.Duration {
	budget := e.opts.RequestTimeout
	if e.fallback == nil {
		// Nothing to reserve time for.
		if deadline, ok := ctx.Deadline(); ok {
			if remaining := time.Until(deadline); remaining > budget {
				budget = remaining
			}
		}
		return budget
	}
	if deadline, ok := ctx.Deadline(); ok {
		if half := time.Until(deadline) / 2; half < budget {
			budget = half
		}
	}
	if budget <= 0 {
		budget = time.Millisecond
	}
	return budget
}

func (e *Engine) record(served, remoteErr string, rtf float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.status.Served = served
	e.status.LastError = remoteErr
	e.status.LastRTF = rtf
}

func responseError(resp control.Response) error {
	if resp.Error != nil {
		return fmt.Errorf("%s: %s", resp.Error.Code, resp.Error.Message)
	}
	return fmt.Errorf("peer rejected the request")
}
