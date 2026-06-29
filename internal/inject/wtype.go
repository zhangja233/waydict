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

	"sway-voice/internal/config"
)

type Wtype struct {
	path      string
	delayMS   int
	timeoutMS int
}

func NewWtype(cfg config.Injection) *Wtype {
	return &Wtype{path: cfg.WtypePath, delayMS: cfg.KeyDelayMS, timeoutMS: cfg.TimeoutMS}
}

func (w *Wtype) Available(context.Context) error {
	if strings.ContainsRune(w.path, os.PathSeparator) {
		st, err := os.Stat(w.path)
		if err != nil {
			return err
		}
		if st.IsDir() {
			return fmt.Errorf("%s is a directory", w.path)
		}
		return nil
	}
	_, err := exec.LookPath(w.path)
	return err
}

func (w *Wtype) TypeText(ctx context.Context, text string) error {
	if text == "" {
		return nil
	}
	timeout := w.timeoutFor(text)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, w.path, "-d", strconv.Itoa(w.delayMS), "-")
	cmd.Stdin = strings.NewReader(text)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("wtype timed out after %s", timeout)
		}
		if msg != "" {
			return fmt.Errorf("wtype failed: %w: %s", err, msg)
		}
		return fmt.Errorf("wtype failed: %w", err)
	}
	return nil
}

func (w *Wtype) timeoutFor(text string) time.Duration {
	min := 2 * time.Second
	delay := time.Duration(utf8.RuneCountInString(text)*(w.delayMS+2))*time.Millisecond + time.Second
	if base := time.Duration(w.timeoutMS) * time.Millisecond; base > delay {
		delay = base
	}
	if delay < min {
		delay = min
	}
	return delay
}
