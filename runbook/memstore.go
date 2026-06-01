package runbook

import (
	"context"
	"sync"
)

// MemoryStore is an in-process [Store] for tests + single-pod dev
// runs. NOT for production — flags evaporate on restart and don't
// propagate across pods.
type MemoryStore struct {
	mu    sync.RWMutex
	flags map[string]bool
}

// NewMemoryStore returns a fresh in-memory Store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{flags: map[string]bool{}}
}

func (s *MemoryStore) Get(_ context.Context, name string) (bool, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.flags[name]
	return v, ok, nil
}

func (s *MemoryStore) Set(_ context.Context, name string, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flags[name] = enabled
	return nil
}

func (s *MemoryStore) All(_ context.Context) (map[string]bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]bool, len(s.flags))
	for k, v := range s.flags {
		out[k] = v
	}
	return out, nil
}
