//go:build !darwin && whispercpp && cgo

package whispercpp

/*
#cgo pkg-config: whisper
*/
import "C"
