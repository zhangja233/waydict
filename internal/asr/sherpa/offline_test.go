//go:build sherpa && cgo

package sherpa

import (
	"context"
	"testing"
	"time"

	"waydict/internal/asr"
	"waydict/internal/config"
)

func TestTranscribeShortSegmentReturnsEmptyWithoutLoad(t *testing.T) {
	engine := New(config.Defaults().ASR)
	tr, err := engine.Transcribe(context.Background(), asr.AudioSegment{
		ID:         "short",
		Samples:    make([]float32, 64),
		SampleRate: 16000,
		StartedAt:  time.Now(),
		Duration:   4 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !tr.Empty || tr.SegmentID != "short" {
		t.Fatalf("transcript = %+v, want empty short segment", tr)
	}
	if engine.Loaded() {
		t.Fatal("short segment should not load the recognizer")
	}
}
