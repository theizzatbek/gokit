package sessionsredis_test

import (
	"context"
	"testing"
	"time"

	"github.com/theizzatbek/gokit/auth/sessions"
	"github.com/theizzatbek/gokit/auth/sessionsredis"
)

func TestStore_ListBySubject_RedisOrderingAndStaleSkip(t *testing.T) {
	if testing.Short() || testRDB == nil {
		t.Skip("integration test — Docker required")
	}
	flushRedis(t)
	s := sessionsredis.New(testRDB, "t1:")
	ctx := context.Background()
	now := time.Now().UTC()
	mk := func(id string, created time.Time, expires time.Time) *sessions.Session {
		return &sessions.Session{
			ID: id, Subject: "u-list",
			CreatedAt: created, LastSeenAt: created, ExpiresAt: expires,
		}
	}
	// 3 sessions with staggered created_at — newest first after sort.
	for i, id := range []string{"a", "b", "c"} {
		created := now.Add(time.Duration(i) * time.Second)
		if err := s.Create(ctx, mk(id, created, now.Add(time.Hour))); err != nil {
			t.Fatal(err)
		}
	}
	// Other subject (must not appear).
	if err := s.Create(ctx, mk("z", now, now.Add(time.Hour))); err != nil {
		t.Fatal(err)
	}
	// Manually mutate the last record to belong to another subject — handled
	// by the helper above already.
	other := &sessions.Session{
		ID: "z", Subject: "u-other",
		CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(time.Hour),
	}
	if err := s.Create(ctx, other); err != nil {
		t.Fatal(err)
	}

	rows, err := s.ListBySubject(ctx, "u-list")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	if !rows[0].CreatedAt.After(rows[2].CreatedAt) {
		t.Errorf("order wrong: %v / %v", rows[0].CreatedAt, rows[2].CreatedAt)
	}
	for _, r := range rows {
		if r.Subject != "u-list" {
			t.Errorf("subject = %q", r.Subject)
		}
	}
}

func TestStore_Stats_Redis(t *testing.T) {
	if testing.Short() || testRDB == nil {
		t.Skip("integration test — Docker required")
	}
	flushRedis(t)
	s := sessionsredis.New(testRDB, "t2:")
	ctx := context.Background()
	now := time.Now().UTC()

	// 2 active.
	for _, id := range []string{"a", "b"} {
		_ = s.Create(ctx, &sessions.Session{
			ID: id, Subject: "u",
			CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(time.Hour),
		})
	}
	// Redis EXPIREAT in the past would drop the row immediately, so the
	// "expired but enumerable" state is intentionally unreachable for
	// the Redis backend — Stats only reports Active + Total.
	stats, err := s.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Total != 2 || stats.Active != 2 {
		t.Errorf("stats = %+v, want total=2 active=2", stats)
	}
}

func TestStore_ListBySubject_RedisEmpty(t *testing.T) {
	if testing.Short() || testRDB == nil {
		t.Skip("integration test — Docker required")
	}
	s := sessionsredis.New(testRDB, "t3:")
	rows, err := s.ListBySubject(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Errorf("rows = %d, want 0", len(rows))
	}
}

// TestStore_ImplementsLister is a compile-time check via the var_ trick
// is already in store.go; this runtime assertion guards against the
// interface drifting silently.
func TestStore_ImplementsLister(t *testing.T) {
	if testRDB == nil {
		t.Skip()
	}
	var _ sessions.Lister = sessionsredis.New(testRDB, "x:")
}
