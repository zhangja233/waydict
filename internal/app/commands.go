package app

import (
	"context"
	"sync"

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
	mu       sync.RWMutex
	texts    []string
	requests []inject.Request
	err      error
}

func (m *MemoryInjector) Backend() string { return "memory" }

func (m *MemoryInjector) Available(context.Context) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.err
}

func (m *MemoryInjector) Inject(ctx context.Context, request inject.Request) error {
	m.mu.RLock()
	err := m.err
	m.mu.RUnlock()
	if err != nil {
		return err
	}
	if request.ValidateTarget != nil {
		if err := request.ValidateTarget(ctx, request.Target.Focus); err != nil {
			return err
		}
	}
	m.mu.Lock()
	m.requests = append(m.requests, request)
	m.texts = append(m.texts, request.Text)
	m.mu.Unlock()
	return nil
}

func (m *MemoryInjector) TypeText(ctx context.Context, text string) error {
	return m.Inject(ctx, inject.Request{Text: text})
}

func (m *MemoryInjector) Texts() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]string(nil), m.texts...)
}

func (m *MemoryInjector) Requests() []inject.Request {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]inject.Request(nil), m.requests...)
}

func (m *MemoryInjector) SetError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.err = err
}

var _ inject.Injector = (*MemoryInjector)(nil)
