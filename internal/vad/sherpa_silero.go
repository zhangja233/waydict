//go:build sherpa && cgo

package vad

import (
	"os"
	"time"

	"waydict/internal/asr"
	"waydict/internal/config"

	onnx "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"
)

type SileroSegmenter struct {
	cfg        config.VAD
	sampleRate int
	vad        sileroVAD
	baseTime   time.Time
	nextID     int
	degraded   bool
}

type sileroVAD interface {
	AcceptWaveform([]float32)
	IsEmpty() bool
	IsSpeech() bool
	Pop()
	Front() *onnx.SpeechSegment
	Reset()
	Flush()
}

func NewSegmenter(cfg config.VAD, sampleRate int) Segmenter {
	if sampleRate == 0 {
		sampleRate = 16000
	}
	if cfg.Engine == "silero" {
		if _, err := os.Stat(cfg.Model); err == nil {
			if s := NewSileroSegmenter(cfg, sampleRate); s != nil {
				return s
			}
		}
	}
	return NewEnergySegmenter(cfg, sampleRate)
}

func NewSileroSegmenter(cfg config.VAD, sampleRate int) *SileroSegmenter {
	v := onnx.NewVoiceActivityDetector(&onnx.VadModelConfig{
		SileroVad: onnx.SileroVadModelConfig{
			Model:              cfg.Model,
			Threshold:          float32(cfg.Threshold),
			MinSilenceDuration: float32(cfg.MinSilenceMS) / 1000,
			MinSpeechDuration:  float32(cfg.MinSpeechMS) / 1000,
			WindowSize:         cfg.WindowSize,
			MaxSpeechDuration:  float32(cfg.MaxSpeechSeconds),
		},
		SampleRate: sampleRate,
		NumThreads: 1,
		Provider:   "cpu",
	}, float32(cfg.MaxSpeechSeconds+cfg.PreRollMS/1000+2))
	if v == nil {
		return nil
	}
	return &SileroSegmenter{cfg: cfg, sampleRate: sampleRate, vad: v}
}

func (s *SileroSegmenter) Feed(samples []float32, now time.Time) []asr.AudioSegment {
	if len(samples) == 0 || s.vad == nil {
		return nil
	}
	if s.baseTime.IsZero() {
		s.baseTime = now.Add(-durationForFrames(s.sampleRate, len(samples)))
	}
	s.vad.AcceptWaveform(samples)
	return s.collect(false)
}

func (s *SileroSegmenter) Flush(commit bool, now time.Time) []asr.AudioSegment {
	if s.vad == nil {
		return nil
	}
	if commit {
		s.vad.Flush()
		out := s.collect(false)
		// sherpa Flush drains audio but retains the model's speech state.
		s.Reset()
		return out
	}
	s.Reset()
	_ = now
	return nil
}

func (s *SileroSegmenter) SegmentOpen() bool {
	return s.vad != nil && s.vad.IsSpeech()
}

func (s *SileroSegmenter) Reset() {
	if s.vad != nil {
		s.vad.Reset()
	}
	s.baseTime = time.Time{}
	s.degraded = false
}

func (s *SileroSegmenter) MarkCaptureOverrun() {
	s.degraded = true
}

func (s *SileroSegmenter) Name() string { return "silero" }

func (s *SileroSegmenter) collect(degraded bool) []asr.AudioSegment {
	var out []asr.AudioSegment
	for !s.vad.IsEmpty() {
		front := s.vad.Front()
		s.vad.Pop()
		if front == nil || len(front.Samples) == 0 {
			continue
		}
		id := "seg-silero-" + itoa6(s.nextID)
		s.nextID++
		started := s.baseTime
		if !started.IsZero() {
			started = started.Add(durationForFrames(s.sampleRate, front.Start))
		}
		samples := append([]float32(nil), front.Samples...)
		out = append(out, asr.AudioSegment{
			ID:             id,
			Samples:        samples,
			SampleRate:     s.sampleRate,
			StartedAt:      started,
			Duration:       durationForFrames(s.sampleRate, len(samples)),
			Degraded:       degraded || s.degraded,
			CaptureOverrun: s.degraded,
		})
		s.degraded = false
	}
	return out
}

func itoa6(v int) string {
	const digits = "0123456789"
	var b [6]byte
	for i := len(b) - 1; i >= 0; i-- {
		b[i] = digits[v%10]
		v /= 10
	}
	return string(b[:])
}
