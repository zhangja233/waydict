//go:build darwin && amd64 && whispercpp && cgo

package whispercpp

/*
#cgo CFLAGS: -I${SRCDIR}/../../../build/whisper/x86_64/include -arch x86_64 -mmacosx-version-min=13.0
#cgo LDFLAGS: -L${SRCDIR}/../../../build/whisper/x86_64/lib -lwhisper -lggml -lggml-cpu -lggml-blas -lggml-metal -lggml-base -framework Metal -framework MetalKit -framework Accelerate -framework Foundation -framework CoreFoundation -lc++ -arch x86_64 -mmacosx-version-min=13.0
*/
import "C"
