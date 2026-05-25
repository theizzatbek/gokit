// Package memstore is an in-process RefreshStore used by auth handler tests.
// NOT for production. Marked internal so external code cannot depend on it.
package memstore

import (
	"context"
	"sync"
	"time"

	"github.com/theizzatbek/gokit/auth"
	"github.com/theizzatbek/gokit/errs"
)

// Compile-time check that *Mem satisfies auth.RefreshStore.
var _ auth.RefreshStore = (*Mem)(nil)

type Mem struct {
	mu       sync.Mutex
	records  map[[32]byte]*auth.Record
	families map[string][][32]byte
	subjects map[string][][32]byte
}

func New() *Mem {
	return &Mem{
		records:  make(map[[32]byte]*auth.Record),
		families: make(map[string][][32]byte),
		subjects: make(map[string][][32]byte),
	}
}

func (m *Mem) Issue(_ context.Context, r auth.Record) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	dup := r
	m.records[r.TokenHash] = &dup
	m.families[r.FamilyID] = append(m.families[r.FamilyID], r.TokenHash)
	if r.Subject != "" {
		m.subjects[r.Subject] = append(m.subjects[r.Subject], r.TokenHash)
	}
	return nil
}

func (m *Mem) Consume(_ context.Context, h [32]byte, now time.Time) (auth.Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.records[h]
	if !ok {
		return auth.Record{}, errs.Unauthorized(auth.CodeRefreshInvalid, "refresh token unknown")
	}
	if rec.RevokedAt != nil || rec.ConsumedAt != nil {
		m.revokeFamilyLocked(rec.FamilyID, now)
		return auth.Record{}, errs.Unauthorized(auth.CodeRefreshReused, "refresh token reused")
	}
	if !rec.ExpiresAt.After(now) {
		return auth.Record{}, errs.Unauthorized(auth.CodeRefreshExpired, "refresh token expired")
	}
	t := now
	rec.ConsumedAt = &t
	return *rec, nil
}

func (m *Mem) RevokeFamily(_ context.Context, familyID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.revokeFamilyLocked(familyID, time.Now())
	return nil
}

func (m *Mem) RevokeSubject(_ context.Context, subject string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	for _, h := range m.subjects[subject] {
		if r, ok := m.records[h]; ok && r.RevokedAt == nil {
			t := now
			r.RevokedAt = &t
		}
	}
	return nil
}

func (m *Mem) GarbageCollect(_ context.Context, now time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for h, r := range m.records {
		if !r.ExpiresAt.After(now) {
			delete(m.records, h)
			n++
		}
	}
	return n, nil
}

func (m *Mem) revokeFamilyLocked(familyID string, now time.Time) {
	for _, h := range m.families[familyID] {
		if r, ok := m.records[h]; ok && r.RevokedAt == nil {
			t := now
			r.RevokedAt = &t
		}
	}
}
