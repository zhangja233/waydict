package app

import (
	"time"

	"waydict/pkg/api"
)

func modePtr(mode api.Mode) *api.Mode {
	m := mode
	return &m
}

func parseMode(value string) (api.Mode, bool) {
	switch value {
	case "", string(api.ModeToggle):
		return api.ModeToggle, true
	case string(api.ModeOneshot):
		return api.ModeOneshot, true
	case string(api.ModeHold):
		return api.ModeHold, true
	default:
		return "", false
	}
}

func asrTimeout(audioDuration time.Duration) time.Duration {
	timeout := 10 * time.Second
	scaled := time.Duration(float64(audioDuration) * 2.5)
	if scaled > timeout {
		timeout = scaled
	}
	return timeout
}
