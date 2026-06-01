package sessions

import (
	"context"
	"sync"
	"time"
)

// MemoryStore is a goroutine-safe in-memory SessionStore for tests
// and single-pod dev deployments. NOT for production — sessions are
// lost on every restart and never garbage-collected.
type MemoryStore struct {
	mu        sync.RWMutex
	byID      map[string]*Session
	bySubject map[string]map[string]struct{}
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		byID:      map[string]*Session{},
		bySubject: map[string]map[string]struct{}{},
	}
}

func (s *MemoryStore) Create(_ context.Context, sess *Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byID[sess.ID] = cloneSession(sess)
	if _, ok := s.bySubject[sess.Subject]; !ok {
		s.bySubject[sess.Subject] = map[string]struct{}{}
	}
	s.bySubject[sess.Subject][sess.ID] = struct{}{}
	return nil
}

func (s *MemoryStore) Get(_ context.Context, id string) (*Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.byID[id]
	if !ok {
		return nil, nil
	}
	return cloneSession(sess), nil
}

func (s *MemoryStore) Touch(_ context.Context, id string, lastSeen, expires time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sess, ok := s.byID[id]; ok {
		sess.LastSeenAt = lastSeen
		sess.ExpiresAt = expires
	}
	return nil
}

func (s *MemoryStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sess, ok := s.byID[id]; ok {
		delete(s.byID, id)
		if set, ok := s.bySubject[sess.Subject]; ok {
			delete(set, id)
			if len(set) == 0 {
				delete(s.bySubject, sess.Subject)
			}
		}
	}
	return nil
}

func (s *MemoryStore) DeleteForSubject(_ context.Context, subject string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id := range s.bySubject[subject] {
		delete(s.byID, id)
	}
	delete(s.bySubject, subject)
	return nil
}

// Snapshot returns a copy of every active session keyed by ID. Test
// convenience — production should query the store directly.
func (s *MemoryStore) Snapshot() map[string]*Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]*Session, len(s.byID))
	for k, v := range s.byID {
		out[k] = cloneSession(v)
	}
	return out
}

func cloneSession(in *Session) *Session {
	if in == nil {
		return nil
	}
	cp := *in
	if in.Claims != nil {
		cp.Claims = append([]byte(nil), in.Claims...)
	}
	if in.Scopes != nil {
		cp.Scopes = append([]string(nil), in.Scopes...)
	}
	if in.Roles != nil {
		cp.Roles = append([]string(nil), in.Roles...)
	}
	return &cp
}
