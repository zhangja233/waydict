//go:build darwin && arm64 && whispercpp && cgo

package whispercpp

/*
#cgo CFLAGS: -I${SRCDIR}/../../../build/whisper/arm64/include -arch arm64 -mmacosx-version-min=13.0
#cgo LDFLAGS: -L${SRCDIR}/../../../build/whisper/arm64/lib -lwhisper -lggml -lggml-cpu -lggml-blas -lggml-metal -lggml-base -framework Metal -framework MetalKit -framework Accelerate -framework Foundation -framework CoreFoundation -lc++ -arch arm64 -mmacosx-version-min=13.0
*/
import "C"
