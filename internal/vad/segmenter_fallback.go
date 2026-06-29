//go:build !sherpa || !cgo

package vad

import "waydict/internal/config"

func NewSegmenter(cfg config.VAD, sampleRate int) Segmenter {
	return NewEnergySegmenter(cfg, sampleRate)
}
