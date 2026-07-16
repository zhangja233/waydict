//go:build !darwin || !coreaudio || !cgo

package buildinfo

const CoreAudioEnabled = false
