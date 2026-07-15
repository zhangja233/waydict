package log

import (
	"io"
	"log/slog"
	"os"
)

func New(level string, out io.Writer) *slog.Logger {
	logger, _ := NewDynamic(level, out)
	return logger
}

func NewDynamic(level string, out io.Writer) (*slog.Logger, *slog.LevelVar) {
	if out == nil {
		out = os.Stderr
	}
	levelVar := &slog.LevelVar{}
	SetLevel(levelVar, level)
	return slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{Level: levelVar})), levelVar
}

func SetLevel(target *slog.LevelVar, level string) {
	if target == nil {
		return
	}
	var parsed slog.Level
	switch level {
	case "debug":
		parsed = slog.LevelDebug
	case "warn":
		parsed = slog.LevelWarn
	case "error":
		parsed = slog.LevelError
	default:
		parsed = slog.LevelInfo
	}
	target.Set(parsed)
}
