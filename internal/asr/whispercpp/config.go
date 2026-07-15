package whispercpp

import (
	"fmt"
	"runtime"
)

type Config struct {
	ModelPath  string
	Device     int
	UseGPU     bool
	NumThreads int
}

func normalizeConfig(cfg Config) (Config, error) {
	if cfg.ModelPath == "" {
		return Config{}, fmt.Errorf("whisper model path is empty")
	}
	if cfg.Device < 0 {
		return Config{}, fmt.Errorf("invalid whisper device %d", cfg.Device)
	}
	if cfg.NumThreads <= 0 {
		cfg.NumThreads = runtime.NumCPU()
	}
	return cfg, nil
}
