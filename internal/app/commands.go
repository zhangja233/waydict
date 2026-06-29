package app

import (
	"context"

	"waydict/internal/asr"
	"waydict/internal/inject"
)

type FakeEngine struct {
	Text     string
	IsLoaded bool
}

func (f *FakeEngine) Name() string { return "fake" }

func (f *FakeEngine) Load(context.Context) error {
	f.IsLoaded = true
	return nil
}

func (f *FakeEngine) Close() error {
	f.IsLoaded = false
	return nil
}

func (f *FakeEngine) Loaded() bool { return f.IsLoaded }

func (f *FakeEngine) Transcribe(_ context.Context, seg asr.AudioSegment) (asr.Transcript, error) {
	return asr.Transcript{
		SegmentID:      seg.ID,
		Text:           f.Text,
		StartedAt:      seg.StartedAt,
		AudioDuration:  seg.Duration,
		DecodeDuration: 0,
		Empty:          f.Text == "",
	}, nil
}

type MemoryInjector struct {
	Texts []string
	Err   error
}

func (m *MemoryInjector) Available(context.Context) error { return m.Err }

func (m *MemoryInjector) TypeText(_ context.Context, text string) error {
	if m.Err != nil {
		return m.Err
	}
	m.Texts = append(m.Texts, text)
	return nil
}

var _ inject.Injector = (*MemoryInjector)(nil)
