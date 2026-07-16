//go:build darwin && (!whispercpp || !cgo)

package main

import (
	"waydict/internal/app"
	"waydict/internal/asr"
)

var appWhisperFactory app.WhisperFactory
var appAcceleratorProbe func(provider string, device int) (asr.Accelerator, error)
