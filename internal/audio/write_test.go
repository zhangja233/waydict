package audio

import (
	"path/filepath"
	"testing"
)

func TestWriteWAVFloat32RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "segment.wav")
	want := []float32{-0.5, 0, 0.5}
	if err := WriteWAVFloat32(path, want, 16000); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.SampleRate != 16000 {
		t.Fatalf("sample rate = %d, want 16000", got.SampleRate)
	}
	if len(got.Samples) != len(want) {
		t.Fatalf("samples = %d, want %d", len(got.Samples), len(want))
	}
	for i := range want {
		if got.Samples[i] != want[i] {
			t.Fatalf("sample %d = %v, want %v", i, got.Samples[i], want[i])
		}
	}
}
