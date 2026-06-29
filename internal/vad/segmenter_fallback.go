//go:build !sherpa || !cgo

package vad

import "sway-voice/internal/config"

func NewSegmenter(cfg config.VAD, sampleRate int) Segmenter {
	return NewEnergySegmenter(cfg, sampleRate)
}
