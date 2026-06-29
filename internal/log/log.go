package log

import (
	"io"
	"log/slog"
	"os"
)

func New(level string, out io.Writer) *slog.Logger {
	if out == nil {
		out = os.Stderr
	}
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{Level: lvl}))
}
