package vad

import (
	"fmt"
	"math"
	"time"

	"sway-voice/internal/asr"
	"sway-voice/internal/config"
)

type EnergySegmenter struct {
	cfg            config.VAD
	sampleRate     int
	speechFrames   int
	silenceFrames  int
	open           bool
	speechSeen     bool
	segment        []float32
	preRoll        []float32
	preRollCap     int
	segmentStart   time.Time
	nextID         int
	forcedOverlap  int
	captureOverrun bool
}

func NewEnergySegmenter(cfg config.VAD, sampleRate int) *EnergySegmenter {
	if sampleRate == 0 {
		sampleRate = 16000
	}
	preRollCap := sampleRate * cfg.PreRollMS / 1000
	return &EnergySegmenter{
		cfg:           cfg,
		sampleRate:    sampleRate,
		preRollCap:    preRollCap,
		forcedOverlap: sampleRate * 300 / 1000,
	}
}

func (s *EnergySegmenter) Feed(samples []float32, now time.Time) []asr.AudioSegment {
	if len(samples) == 0 {
		return nil
	}
	s.updatePreRoll(samples)
	isSpeech := rms(samples) >= s.threshold()
	if isSpeech {
		s.speechFrames += len(samples)
		s.silenceFrames = 0
	} else {
		s.silenceFrames += len(samples)
		if !s.open {
			s.speechFrames = 0
		}
	}
	var out []asr.AudioSegment
	minSpeech := s.sampleRate * s.cfg.MinSpeechMS / 1000
	if !s.open && s.speechFrames >= minSpeech {
		s.open = true
		s.speechSeen = true
		s.segmentStart = now.Add(-durationForFrames(s.sampleRate, len(s.preRoll)+s.speechFrames))
		s.segment = append(s.segment[:0], s.preRoll...)
	}
	if s.open {
		s.segment = append(s.segment, samples...)
	}
	maxFrames := s.sampleRate * s.cfg.MaxSpeechSeconds
	if s.open && len(s.segment) >= maxFrames {
		out = append(out, s.close(now, true))
		s.startOverlap(now)
		return out
	}
	minSilence := s.sampleRate * s.cfg.MinSilenceMS / 1000
	pad := s.sampleRate * s.cfg.SpeechPadMS / 1000
	if s.open && s.silenceFrames >= minSilence+pad {
		if seg, ok := s.closeIfLongEnough(now, false); ok {
			out = append(out, seg)
		} else {
			s.resetOpen()
		}
	}
	return out
}

func (s *EnergySegmenter) Flush(commit bool, now time.Time) []asr.AudioSegment {
	if !commit || !s.open {
		s.Reset()
		return nil
	}
	seg := s.close(now, false)
	s.Reset()
	return []asr.AudioSegment{seg}
}

func (s *EnergySegmenter) SegmentOpen() bool {
	return s.open
}

func (s *EnergySegmenter) Reset() {
	s.speechFrames = 0
	s.silenceFrames = 0
	s.resetOpen()
	s.preRoll = nil
}

func (s *EnergySegmenter) MarkCaptureOverrun() {
	s.captureOverrun = true
}

func (s *EnergySegmenter) updatePreRoll(samples []float32) {
	if s.preRollCap <= 0 {
		return
	}
	s.preRoll = append(s.preRoll, samples...)
	if excess := len(s.preRoll) - s.preRollCap; excess > 0 {
		copy(s.preRoll, s.preRoll[excess:])
		s.preRoll = s.preRoll[:s.preRollCap]
	}
}

func (s *EnergySegmenter) closeIfLongEnough(now time.Time, degraded bool) (asr.AudioSegment, bool) {
	if len(s.segment) < s.sampleRate*300/1000 {
		return asr.AudioSegment{}, false
	}
	return s.close(now, degraded), true
}

func (s *EnergySegmenter) close(now time.Time, degraded bool) asr.AudioSegment {
	id := fmt.Sprintf("seg-%06d", s.nextID)
	s.nextID++
	samples := append([]float32(nil), s.segment...)
	seg := asr.AudioSegment{
		ID:             id,
		Samples:        samples,
		SampleRate:     s.sampleRate,
		StartedAt:      s.segmentStart,
		Duration:       durationForFrames(s.sampleRate, len(samples)),
		Degraded:       degraded || s.captureOverrun,
		CaptureOverrun: s.captureOverrun,
	}
	_ = now
	s.resetOpen()
	return seg
}

func (s *EnergySegmenter) startOverlap(now time.Time) {
	overlap := s.forcedOverlap
	if overlap > len(s.preRoll) {
		overlap = len(s.preRoll)
	}
	if overlap > 0 {
		s.open = true
		s.speechSeen = true
		s.segmentStart = now.Add(-durationForFrames(s.sampleRate, overlap))
		s.segment = append(s.segment[:0], s.preRoll[len(s.preRoll)-overlap:]...)
	}
}

func (s *EnergySegmenter) resetOpen() {
	s.open = false
	s.speechSeen = false
	s.segment = nil
	s.captureOverrun = false
}

func (s *EnergySegmenter) threshold() float64 {
	if s.cfg.Threshold <= 0 {
		return 0.02
	}
	if s.cfg.Threshold > 0.2 {
		return 0.02
	}
	return s.cfg.Threshold
}

func rms(samples []float32) float64 {
	if len(samples) == 0 {
		return 0
	}
	var sum float64
	for _, sample := range samples {
		v := float64(sample)
		sum += v * v
	}
	return math.Sqrt(sum / float64(len(samples)))
}

func durationForFrames(sampleRate, frames int) time.Duration {
	return time.Duration(frames) * time.Second / time.Duration(sampleRate)
}
