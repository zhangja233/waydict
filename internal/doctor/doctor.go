package doctor

import (
	"context"
	"fmt"
	"time"

	"waydict/internal/asr"
	remoteasr "waydict/internal/asr/remote"
	"waydict/internal/config"
)

type Level string

const (
	OK   Level = "OK"
	Fail Level = "FAIL"
	Warn Level = "WARN"
	Info Level = "INFO"
)

type Result struct {
	Level  Level
	Name   string
	Detail string
	Err    error
}

type Registry interface {
	Checks(context.Context, config.Config) []Result
}

func errorResult(name string, err error) Result {
	if err != nil {
		return Result{Level: Fail, Name: name, Err: err}
	}
	return Result{Level: OK, Name: name}
}

// remoteASRResults probes the peer behind engine = "remote". An unreachable
// peer is a warning, not a failure, when a fallback is configured: dictation
// still works, just on local CPU. It is only fatal with fallback = "none".
func remoteASRResults(ctx context.Context, cfg config.Config) []Result {
	if cfg.ASR.Engine != asr.EngineRemote {
		return nil
	}
	engine := remoteasr.New(remoteasr.OptionsFromConfig(cfg), nil)
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// The renderer prints Detail only for non-OK levels, so a reachable peer
	// needs no detail; status --json carries the socket either way.
	if err := engine.Reachable(probeCtx); err != nil {
		if cfg.ASR.Remote.Fallback != asr.FallbackNone {
			return []Result{{Name: "remote ASR", Level: Warn, Err: err, Detail: fmt.Sprintf(
				"%s unreachable; decoding falls back to %s", cfg.ASR.Remote.Socket, cfg.ASR.Remote.Fallback)}}
		}
		return []Result{{Name: "remote ASR", Level: Fail, Err: err}}
	}
	return []Result{{Name: "remote ASR", Level: OK}}
}
