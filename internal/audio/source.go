package audio

import (
	"context"
	"errors"
	"time"
)

var ErrUnavailable = errors.New("pipewire capture unavailable")

type Stats struct {
	SampleRate int
	LevelDBFS  float64
	Overruns   uint64
	Capturing  bool
}

type Source interface {
	Start(context.Context) error
	Pause(context.Context) error
	Stop(context.Context) error
	Read(ctx context.Context, dst []float32) (int, error)
	Stats() Stats
}

type ScriptedSource struct {
	SampleRate int
	Chunks     [][]float32
	Delay      time.Duration
	capturing  bool
	index      int
}

func (s *ScriptedSource) Start(context.Context) error {
	s.capturing = true
	return nil
}

func (s *ScriptedSource) Pause(context.Context) error {
	s.capturing = false
	return nil
}

func (s *ScriptedSource) Stop(context.Context) error {
	s.capturing = false
	s.index = 0
	return nil
}

func (s *ScriptedSource) Read(ctx context.Context, dst []float32) (int, error) {
	if !s.capturing {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(s.Delay):
			return 0, nil
		}
	}
	if s.index >= len(s.Chunks) {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(s.Delay):
			return 0, nil
		}
	}
	chunk := s.Chunks[s.index]
	s.index++
	n := copy(dst, chunk)
	return n, nil
}

func (s *ScriptedSource) Stats() Stats {
	rate := s.SampleRate
	if rate == 0 {
		rate = 16000
	}
	return Stats{SampleRate: rate, LevelDBFS: LevelDBFS(lastChunk(s.Chunks, s.index)), Capturing: s.capturing}
}

func lastChunk(chunks [][]float32, index int) []float32 {
	if index <= 0 || index > len(chunks) {
		return nil
	}
	return chunks[index-1]
}
