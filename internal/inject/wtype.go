package inject

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"waydict/internal/apperr"
	"waydict/internal/config"
)

type Wtype struct {
	path      string
	delayMS   int
	timeoutMS int
}

func NewWtype(cfg config.Injection) *Wtype {
	return &Wtype{path: cfg.WtypePath, delayMS: cfg.KeyDelayMS, timeoutMS: cfg.TimeoutMS}
}

func (w *Wtype) Backend() string { return "wtype" }

func (w *Wtype) Available(context.Context) error {
	if strings.ContainsRune(w.path, os.PathSeparator) {
		st, err := os.Stat(w.path)
		if err != nil {
			return apperr.New(apperr.CodeInjectorUnavailable, "check wtype", err)
		}
		if st.IsDir() {
			return apperr.New(apperr.CodeInjectorUnavailable, "check wtype", fmt.Errorf("%s is a directory", w.path))
		}
		if st.Mode()&0111 == 0 {
			return apperr.New(apperr.CodeInjectorUnavailable, "check wtype", fmt.Errorf("%s is not executable", w.path))
		}
		return nil
	}
	_, err := exec.LookPath(w.path)
	if err != nil {
		return apperr.New(apperr.CodeInjectorUnavailable, "check wtype", err)
	}
	return nil
}

func (w *Wtype) Inject(ctx context.Context, req Request) error {
	if req.ValidateTarget != nil {
		if err := req.ValidateTarget(ctx, req.Target.Focus); err != nil {
			return err
		}
	}
	delayMS := w.delayMS
	if req.KeyDelay > 0 {
		delayMS = int(req.KeyDelay / time.Millisecond)
	}
	return w.typeText(ctx, req.Text, delayMS, req.Deadline)
}

// TypeText remains available to non-app callers that do not own a focus target.
func (w *Wtype) TypeText(ctx context.Context, text string) error {
	return w.typeText(ctx, text, w.delayMS, time.Time{})
}

func (w *Wtype) typeText(ctx context.Context, text string, delayMS int, deadline time.Time) error {
	if text == "" {
		return nil
	}
	timeout := w.timeoutFor(text, delayMS)
	if !deadline.IsZero() {
		if remaining := time.Until(deadline); remaining < timeout {
			timeout = remaining
		}
	}
	if timeout <= 0 {
		return apperr.New(apperr.CodeInjectionFailed, "run wtype", context.DeadlineExceeded)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, w.path, "-d", strconv.Itoa(delayMS), "-")
	cmd.Stdin = strings.NewReader(text)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return apperr.New(apperr.CodeInjectionFailed, "run wtype", fmt.Errorf("timed out after %s", timeout))
		}
		if msg != "" {
			return apperr.New(apperr.CodeInjectionFailed, "run wtype", fmt.Errorf("%w: %s", err, msg))
		}
		return apperr.New(apperr.CodeInjectionFailed, "run wtype", err)
	}
	return nil
}

func (w *Wtype) timeoutFor(text string, delayMS int) time.Duration {
	min := 2 * time.Second
	delay := time.Duration(utf8.RuneCountInString(text)*(delayMS+2))*time.Millisecond + time.Second
	if base := time.Duration(w.timeoutMS) * time.Millisecond; base > delay {
		delay = base
	}
	if delay < min {
		delay = min
	}
	return delay
}
