package asr

import (
	"math"
	"strings"
	"testing"
	"time"
)

func TestPCMS16LERoundTrip(t *testing.T) {
	samples := []float32{0, 0.5, -0.5, 0.999, -1}
	decoded, err := DecodePCMS16LE(EncodePCMS16LE(samples))
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded) != len(samples) {
		t.Fatalf("decoded %d samples, want %d", len(decoded), len(samples))
	}
	for i, want := range samples {
		// int16 quantization costs at most one step.
		if math.Abs(float64(decoded[i]-want)) > 1.0/32768 {
			t.Fatalf("sample %d = %v, want ~%v", i, decoded[i], want)
		}
	}
}

// A clipping microphone must not wrap to the opposite rail, which would turn
// overload into loud noise the model then tries to transcribe.
func TestEncodePCMS16LEClampsInsteadOfWrapping(t *testing.T) {
	decoded, err := DecodePCMS16LE(EncodePCMS16LE([]float32{4, -4}))
	if err != nil {
		t.Fatal(err)
	}
	if decoded[0] <= 0 || decoded[1] >= 0 {
		t.Fatalf("clipped samples wrapped: %v", decoded)
	}
}

func TestDecodePCMS16LERejectsOddLength(t *testing.T) {
	if _, err := DecodePCMS16LE([]byte{1, 2, 3}); err == nil {
		t.Fatal("expected an error for a partial sample")
	}
}

func TestSegmentRoundTrip(t *testing.T) {
	segment := AudioSegment{
		ID:             "seg-1",
		Samples:        []float32{0.25, -0.25},
		SampleRate:     16000,
		Duration:       1500 * time.Millisecond,
		Degraded:       true,
		CaptureOverrun: true,
	}
	args := jsonRoundTrip(t, EncodeSegmentArgs(segment, CodecPCMS16LE))
	decoded, err := DecodeSegment(args, EncodePCMS16LE(segment.Samples))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.ID != segment.ID || decoded.SampleRate != segment.SampleRate ||
		decoded.Duration != segment.Duration || !decoded.Degraded || !decoded.CaptureOverrun {
		t.Fatalf("segment round-trip mismatch: %+v", decoded)
	}
	if len(decoded.Samples) != len(segment.Samples) {
		t.Fatalf("decoded %d samples, want %d", len(decoded.Samples), len(segment.Samples))
	}
}

func TestDecodeSegmentRejectsUnknownCodec(t *testing.T) {
	args := jsonRoundTrip(t, EncodeSegmentArgs(AudioSegment{SampleRate: 16000}, "opus"))
	_, err := DecodeSegment(args, nil)
	if err == nil || !strings.Contains(err.Error(), "unsupported codec") {
		t.Fatalf("error = %v, want an unsupported-codec rejection", err)
	}
}

func TestDecodeSegmentRejectsInvalidSampleRate(t *testing.T) {
	args := jsonRoundTrip(t, EncodeSegmentArgs(AudioSegment{SampleRate: 0}, CodecPCMS16LE))
	if _, err := DecodeSegment(args, nil); err == nil {
		t.Fatal("expected an error for a zero sample rate")
	}
}

func TestTranscriptRoundTrip(t *testing.T) {
	started := time.Now()
	want := Transcript{
		SegmentID:       "seg-1",
		Text:            "hello there",
		Tokens:          []string{"hello", "there"},
		TokenTimestamps: []float64{0, 0.4},
		AudioDuration:   2 * time.Second,
		DecodeDuration:  250 * time.Millisecond,
		RealTimeFactor:  0.125,
	}
	data := jsonRoundTrip(t, EncodeTranscript(want))
	got, err := DecodeTranscript(data, AudioSegment{StartedAt: started})
	if err != nil {
		t.Fatal(err)
	}
	if got.SegmentID != want.SegmentID || got.Text != want.Text ||
		got.AudioDuration != want.AudioDuration || got.DecodeDuration != want.DecodeDuration ||
		got.RealTimeFactor != want.RealTimeFactor || len(got.Tokens) != 2 || len(got.TokenTimestamps) != 2 {
		t.Fatalf("transcript round-trip mismatch: %+v", got)
	}
	// StartedAt never crosses the wire; the caller's own segment supplies it.
	if !got.StartedAt.Equal(started) {
		t.Fatalf("StartedAt = %v, want %v", got.StartedAt, started)
	}
}

// jsonRoundTrip mimics the trip through the control socket, where every number
// arrives as float64 rather than the int type that was encoded.
func jsonRoundTrip(t *testing.T, value map[string]any) map[string]any {
	t.Helper()
	var out map[string]any
	if err := remarshal(value, &out); err != nil {
		t.Fatal(err)
	}
	return out
}
