package audit

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
)

// MemoryStore is an in-process Store for tests and single-pod dev
// runs. Goroutine-safe. NOT for production — events are lost on
// restart and there is no off-process tamper-evidence.
type MemoryStore struct {
	mu     sync.Mutex
	events []Event
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore { return &MemoryStore{} }

// Append stores e (by value).
func (s *MemoryStore) Append(_ context.Context, e *Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, *e)
	return nil
}

// ChainLock returns a no-op release — MemoryStore is in-process,
// the Logger's chainMu is sufficient.
func (s *MemoryStore) ChainLock(_ context.Context) (func(), error) {
	return func() {}, nil
}

// Query returns events matching f ordered by OccurredAt ASC.
func (s *MemoryStore) Query(_ context.Context, f Filter) ([]Event, error) {
	s.mu.Lock()
	out := make([]Event, 0, len(s.events))
	for _, e := range s.events {
		if !matches(e, f) {
			continue
		}
		out = append(out, e)
	}
	s.mu.Unlock()
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].OccurredAt.Before(out[j].OccurredAt)
	})
	if f.Offset > 0 {
		if f.Offset >= len(out) {
			return nil, nil
		}
		out = out[f.Offset:]
	}
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, nil
}

// LastHash returns the Hash of the most recent event (by
// OccurredAt). Empty store → nil.
func (s *MemoryStore) LastHash(_ context.Context) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.events) == 0 {
		return nil, nil
	}
	var latest Event
	for _, e := range s.events {
		if latest.OccurredAt.Before(e.OccurredAt) {
			latest = e
		}
	}
	return latest.Hash, nil
}

// PurgeBefore drops events with OccurredAt < t.
func (s *MemoryStore) PurgeBefore(_ context.Context, t time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.events[:0]
	var removed int64
	for _, e := range s.events {
		if e.OccurredAt.Before(t) {
			removed++
			continue
		}
		kept = append(kept, e)
	}
	s.events = kept
	return removed, nil
}

// Snapshot returns a copy of every event. Tests-only convenience —
// production callers should use Query.
func (s *MemoryStore) Snapshot() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Event, len(s.events))
	copy(out, s.events)
	return out
}

// matches reports whether e satisfies f. Empty fields in f are
// wildcards.
func matches(e Event, f Filter) bool {
	if f.Actor != "" && e.Actor.Subject != f.Actor {
		return false
	}
	if f.Action != "" && !actionMatches(e.Action, f.Action) {
		return false
	}
	if f.TargetType != "" && e.Target.Type != f.TargetType {
		return false
	}
	if f.TargetID != "" && e.Target.ID != f.TargetID {
		return false
	}
	if f.Outcome != "" && e.Outcome != f.Outcome {
		return false
	}
	if !f.From.IsZero() && e.OccurredAt.Before(f.From) {
		return false
	}
	if !f.To.IsZero() && e.OccurredAt.After(f.To) {
		return false
	}
	return true
}

// actionMatches supports the trailing "*" wildcard.
func actionMatches(action, pat string) bool {
	if pat == action {
		return true
	}
	if strings.HasSuffix(pat, "*") {
		prefix := strings.TrimSuffix(pat, "*")
		return strings.HasPrefix(action, prefix)
	}
	return false
}
