package preferences

import (
	"context"
	"sync"
)

const (
	KeyUISchemaVersion            = "uiSchemaVersion"
	KeyOnboardingCompletedVersion = "onboardingCompletedVersion"
	KeySelectedAudioDeviceUID     = "selectedAudioDeviceUID"
	KeySelectedHotkeyMode         = "selectedHotkeyMode"
)

type Store interface {
	String(context.Context, string) (string, bool, error)
	SetString(context.Context, string, string) error
	Delete(context.Context, string) error
}

type MemoryStore struct {
	mu     sync.Mutex
	Values map[string]string
	Err    error
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{Values: make(map[string]string)}
}

func (s *MemoryStore) String(_ context.Context, key string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return "", false, s.Err
	}
	value, ok := s.Values[key]
	return value, ok, nil
}

func (s *MemoryStore) SetString(_ context.Context, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return s.Err
	}
	if s.Values == nil {
		s.Values = make(map[string]string)
	}
	s.Values[key] = value
	return nil
}

func (s *MemoryStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return s.Err
	}
	delete(s.Values, key)
	return nil
}
