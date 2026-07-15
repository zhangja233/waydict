//go:build whispercpp && cgo

package whispercpp_test

import (
	"context"
	"os"
	"runtime"
	"strings"
	"testing"

	"waydict/internal/asr"
	"waydict/internal/asr/whispercpp"
	"waydict/internal/audio"
)

func TestIntegrationTranscribe(t *testing.T) {
	modelPath := os.Getenv("WAYDICT_TEST_WHISPER_MODEL")
	if modelPath == "" {
		t.Skip("WAYDICT_TEST_WHISPER_MODEL is not set")
	}
	wavPath := os.Getenv("WAYDICT_TEST_WAV")
	if wavPath == "" {
		t.Fatal("WAYDICT_TEST_WAV is not set")
	}
	audioFile, err := audio.ReadFile(wavPath)
	if err != nil {
		t.Fatal(err)
	}
	threads := runtime.NumCPU()
	if threads > 8 {
		threads = 8
	}
	engine, err := whispercpp.New(whispercpp.Config{
		ModelPath:  modelPath,
		UseGPU:     os.Getenv("WAYDICT_TEST_WHISPER_GPU") == "1",
		NumThreads: threads,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()

	transcript, err := engine.Transcribe(context.Background(), asr.AudioSegment{
		ID:         "integration",
		Samples:    audioFile.Samples,
		SampleRate: audioFile.SampleRate,
		Duration:   audioFile.Duration,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(transcript.Text) == "" || transcript.Empty {
		t.Fatalf("empty transcript: %+v", transcript)
	}
	if transcript.SegmentID != "integration" || transcript.AudioDuration != audioFile.Duration {
		t.Fatalf("transcript metadata = %+v", transcript)
	}
	if len(transcript.Tokens) == 0 || len(transcript.TokenTimestamps) != 0 {
		t.Fatalf("transcript tokens = %q, timestamps = %v", transcript.Tokens, transcript.TokenTimestamps)
	}
	if transcript.DecodeDuration <= 0 || transcript.RealTimeFactor <= 0 {
		t.Fatalf("decode duration = %s, RTF = %f", transcript.DecodeDuration, transcript.RealTimeFactor)
	}
	name, gpu := engine.ActiveBackend()
	if name == "" {
		t.Fatal("native logs did not identify the active backend")
	}
	if wantGPU := os.Getenv("WAYDICT_TEST_WHISPER_GPU") == "1"; gpu != wantGPU {
		t.Fatalf("active backend = (%q, %t), want gpu=%t", name, gpu, wantGPU)
	}
}
