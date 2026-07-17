//go:build sherpa && cgo

package vad

import (
	"testing"
	"time"

	onnx "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"
)

func TestSileroCommittedFlushResetsNativeState(t *testing.T) {
	native := &staleFlushVAD{}
	s := &SileroSegmenter{
		sampleRate: 16000,
		vad:        native,
		baseTime:   time.Unix(1, 0),
	}

	segments := s.Flush(true, time.Now())
	if len(segments) != 1 || len(segments[0].Samples) != 3 {
		t.Fatalf("committed segments = %#v, want one 3-sample segment", segments)
	}
	if native.resetCalls != 1 {
		t.Fatalf("native reset calls = %d, want 1", native.resetCalls)
	}
	if !s.baseTime.IsZero() {
		t.Fatalf("base time = %v, want zero after committed flush", s.baseTime)
	}
	if segments := s.Feed([]float32{1}, time.Now()); len(segments) != 0 {
		t.Fatalf("next session produced stale segments: %#v", segments)
	}
}

func TestSileroCollectDropsEmptyNativeSegment(t *testing.T) {
	native := &staleFlushVAD{segments: []*onnx.SpeechSegment{
		{Start: 10},
		{Start: 20, Samples: []float32{1, 2}},
	}}
	s := &SileroSegmenter{sampleRate: 16000, vad: native}

	segments := s.collect(false)
	if len(segments) != 1 || len(segments[0].Samples) != 2 {
		t.Fatalf("segments = %#v, want only the non-empty segment", segments)
	}
}

type staleFlushVAD struct {
	segments   []*onnx.SpeechSegment
	stale      bool
	resetCalls int
}

func (v *staleFlushVAD) AcceptWaveform([]float32) {
	if v.stale {
		v.segments = append(v.segments, &onnx.SpeechSegment{})
	}
}

func (v *staleFlushVAD) IsEmpty() bool  { return len(v.segments) == 0 }
func (v *staleFlushVAD) IsSpeech() bool { return false }
func (v *staleFlushVAD) Pop()           { v.segments = v.segments[1:] }
func (v *staleFlushVAD) Front() *onnx.SpeechSegment {
	return v.segments[0]
}
func (v *staleFlushVAD) Flush() {
	v.segments = append(v.segments, &onnx.SpeechSegment{Samples: []float32{1, 2, 3}})
	v.stale = true
}
func (v *staleFlushVAD) Reset() {
	v.stale = false
	v.resetCalls++
}
