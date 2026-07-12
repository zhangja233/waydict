//go:build whispercpp && cgo

package whispercpp

/*
#include <stdlib.h>
*/
import "C"

//export waydictWhisperLog
func waydictWhisperLog(_ C.int, text *C.char) {
	if text == nil {
		return
	}
	activeCaptureMu.Lock()
	capture := activeCapture
	activeCaptureMu.Unlock()
	if capture != nil {
		capture.observe(C.GoString(text))
	}
}
