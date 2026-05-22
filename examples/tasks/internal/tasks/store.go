package tasks

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"sync"
	"time"
)

// ErrNotFound is returned by Store methods when the requested task
// doesn't exist (or doesn't belong to the caller's owner).
var ErrNotFound = errors.New("task not found")

// Store is the persistence interface so handlers can be tested
// without an in-memory map. Swap for a Postgres-backed impl by
// implementing the same methods.
//
// Owner-scoped methods (Get/ListByOwner/Update/Delete) enforce
// "users only touch their own data". The AdminDelete escape hatch
// exists so admin-role handlers can override that — authorization
// at the route level is necessary but not sufficient.
type Store interface {
	ListByOwner(ownerID string) []Task
	Get(ownerID, id string) (Task, error)
	Create(ownerID, title string) Task
	Update(ownerID, id string, title *string, done *bool) (Task, error)
	Delete(ownerID, id string) error
	AdminDelete(id string) error
}

// memStore is the demo in-memory implementation. Safe for concurrent
// use. The mutex granularity is "entire store" — fine for a demo,
// not for production load.
type memStore struct {
	mu    sync.RWMutex
	tasks map[string]Task // id -> Task
	now   func() time.Time
}

// NewMemStore returns a fresh empty in-memory store.
func NewMemStore() Store {
	return &memStore{
		tasks: make(map[string]Task),
		now:   time.Now,
	}
}

func (s *memStore) ListByOwner(ownerID string) []Task {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Task
	for _, t := range s.tasks {
		if t.OwnerID == ownerID {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

func (s *memStore) Get(ownerID, id string) (Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.tasks[id]
	if !ok || t.OwnerID != ownerID {
		return Task{}, ErrNotFound
	}
	return t, nil
}

func (s *memStore) Create(ownerID, title string) Task {
	t := Task{
		ID:        newID(),
		OwnerID:   ownerID,
		Title:     title,
		CreatedAt: s.now(),
		UpdatedAt: s.now(),
	}
	s.mu.Lock()
	s.tasks[t.ID] = t
	s.mu.Unlock()
	return t
}

func (s *memStore) Update(ownerID, id string, title *string, done *bool) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok || t.OwnerID != ownerID {
		return Task{}, ErrNotFound
	}
	if title != nil {
		t.Title = *title
	}
	if done != nil {
		t.Done = *done
	}
	t.UpdatedAt = s.now()
	s.tasks[id] = t
	return t, nil
}

func (s *memStore) Delete(ownerID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok || t.OwnerID != ownerID {
		return ErrNotFound
	}
	delete(s.tasks, id)
	return nil
}

func (s *memStore) AdminDelete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tasks[id]; !ok {
		return ErrNotFound
	}
	delete(s.tasks, id)
	return nil
}

func newID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
