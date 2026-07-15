//go:build coreaudio && cgo && darwin

package coreaudio

import (
	"context"
	"errors"
	"math"
	"testing"

	"waydict/internal/audio"
	"waydict/internal/config"
)

func TestNativeFloatRing(t *testing.T) {
	if result := testRing(); result != 0 {
		t.Fatalf("native ring self-test returned %d", result)
	}
}

func TestNativeTapGate(t *testing.T) {
	if result := testTapGate(); result != 0 {
		t.Fatalf("native tap gate self-test returned %d", result)
	}
}

func TestNativeTeardownTimeout(t *testing.T) {
	result, elapsedMS := testTeardownTimeout()
	if result != 0 {
		t.Fatalf("native teardown timeout self-test returned %d", result)
	}
	if elapsedMS < 200 || elapsedMS > 350 {
		t.Fatalf("native teardown returned after %d ms, want approximately 250 ms", elapsedMS)
	}
}

func TestNativeOwnerIdleAndStoppedRead(t *testing.T) {
	cfg := config.Defaults()
	source, err := New(cfg.Audio)
	if err != nil {
		t.Fatal(err)
	}
	capture := source.(*Capture)
	defer capture.Close()
	if stats := capture.Stats(); stats.Backend != "coreaudio" || stats.SampleRate != 16000 || stats.Capturing {
		t.Fatalf("unexpected idle stats: %+v", stats)
	}
	buffer := make([]float32, 32)
	if count, err := capture.Read(context.Background(), buffer); err != nil || count != 0 {
		t.Fatalf("idle read = %d, %v", count, err)
	}
	if err := capture.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := capture.Stop(context.Background()); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
	if _, err := capture.Read(context.Background(), buffer); !errors.Is(err, audio.ErrUnavailable) {
		t.Fatalf("stopped read error = %v", err)
	}
}

func TestSyntheticConversionIdentity(t *testing.T) {
	input := make([]float32, 1024)
	for i := range input {
		input[i] = float32(0.5 * math.Sin(2*math.Pi*440*float64(i)/16000))
	}
	output, err := testConvert(input, len(input), 1, 16000)
	if err != nil {
		t.Fatal(err)
	}
	if len(output) != len(input) {
		t.Fatalf("output frames = %d, want %d", len(output), len(input))
	}
	for i := range input {
		if difference := math.Abs(float64(output[i] - input[i])); difference > 1e-5 {
			t.Fatalf("output[%d] = %f, want %f (difference %g)", i, output[i], input[i], difference)
		}
	}
}

func TestSyntheticConversionDownmixAndResample(t *testing.T) {
	const (
		inputRate   = 48000
		inputFrames = 4800
		channels    = 2
		wantValue   = 0.25
	)
	input := make([]float32, inputFrames*channels)
	for frame := 0; frame < inputFrames; frame++ {
		input[frame*channels] = 0.5
		input[frame*channels+1] = 0
	}
	output, err := testConvert(input, inputFrames, channels, inputRate)
	if err != nil {
		t.Fatal(err)
	}
	if len(output) < 1568 || len(output) > 1632 {
		t.Fatalf("output frames = %d, want approximately 1600", len(output))
	}
	for i := 64; i < len(output)-64; i++ {
		if difference := math.Abs(float64(output[i] - wantValue)); difference > 2e-3 {
			t.Fatalf("output[%d] = %f, want %f (difference %g)", i, output[i], wantValue, difference)
		}
		if output[i] < -1 || output[i] > 1 {
			t.Fatalf("output[%d] = %f outside [-1, 1]", i, output[i])
		}
	}
}

func TestSyntheticConversionClampsNonFiniteSamples(t *testing.T) {
	input := []float32{2, -2, float32(math.NaN()), float32(math.Inf(1)), float32(math.Inf(-1))}
	output, err := testConvert(input, len(input), 1, 16000)
	if err != nil {
		t.Fatal(err)
	}
	if len(output) != len(input) {
		t.Fatalf("output frames = %d, want %d", len(output), len(input))
	}
	for i, sample := range output {
		if math.IsNaN(float64(sample)) || math.IsInf(float64(sample), 0) || sample < -1 || sample > 1 {
			t.Fatalf("output[%d] = %f outside finite [-1, 1]", i, sample)
		}
	}
}
