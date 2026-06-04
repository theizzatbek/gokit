package refreshpg

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/theizzatbek/gokit/auth"
	"github.com/theizzatbek/gokit/errs"
)

// counterValue gathers `name{op,outcome}` from a registry.
func counterValue(t *testing.T, reg *prometheus.Registry, name, op, outcome string) float64 {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.Metric {
			labels := labelMap(m)
			if labels["op"] == op && (outcome == "" || labels["outcome"] == outcome) {
				return m.GetCounter().GetValue()
			}
		}
	}
	return 0
}

func labelMap(m *dto.Metric) map[string]string {
	out := map[string]string{}
	for _, l := range m.GetLabel() {
		out[l.GetName()] = l.GetValue()
	}
	return out
}

func TestSessionState_Classification(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		info SessionInfo
		want string
	}{
		{"active", SessionInfo{ExpiresAt: now.Add(time.Hour)}, "active"},
		{"expired", SessionInfo{ExpiresAt: now.Add(-time.Hour)}, "expired"},
		{"consumed", SessionInfo{ConsumedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour)}, "consumed"},
		{"revoked-wins-over-consumed", SessionInfo{ConsumedAt: now, RevokedAt: now, ExpiresAt: now.Add(time.Hour)}, "revoked"},
		{"revoked-wins-over-expired", SessionInfo{RevokedAt: now, ExpiresAt: now.Add(-time.Hour)}, "revoked"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sessionState(tc.info, now); got != tc.want {
				t.Errorf("state = %q, want %q", got, tc.want)
			}
		})
	}
}

func makeRecord(seed byte, subject, family, ip string, ttl time.Duration) auth.Record {
	var h [32]byte
	h[0] = seed
	now := time.Now().UTC().Truncate(time.Second)
	return auth.Record{
		TokenHash: h, Subject: subject, FamilyID: family,
		IssuedAt: now, ExpiresAt: now.Add(ttl),
		UserAgent: "ua-" + subject, IP: ip,
	}
}

func TestStore_Stats(t *testing.T) {
	if testing.Short() || testDB == nil {
		t.Skip("integration test — Docker required")
	}
	ctx := context.Background()
	if _, err := testDB.Exec(ctx, "TRUNCATE auth_refresh_tokens"); err != nil {
		t.Fatal(err)
	}
	s := New(testDB)
	// active
	if err := s.Issue(ctx, makeRecord(1, "u-a", "11111111-1111-1111-1111-111111111111", "10.0.0.1", time.Hour)); err != nil {
		t.Fatal(err)
	}
	// expired
	exp := makeRecord(2, "u-a", "22222222-2222-2222-2222-222222222222", "10.0.0.1", time.Hour)
	exp.ExpiresAt = time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	if err := s.Issue(ctx, exp); err != nil {
		t.Fatal(err)
	}
	// consumed
	con := makeRecord(3, "u-b", "33333333-3333-3333-3333-333333333333", "10.0.0.2", time.Hour)
	if err := s.Issue(ctx, con); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Consume(ctx, con.TokenHash, time.Now()); err != nil {
		t.Fatalf("Consume: %v", err)
	}
	// revoked
	rev := makeRecord(4, "u-c", "44444444-4444-4444-4444-444444444444", "10.0.0.3", time.Hour)
	if err := s.Issue(ctx, rev); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeFamily(ctx, rev.FamilyID); err != nil {
		t.Fatal(err)
	}
	stats, err := s.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Active != 1 || stats.Expired != 1 || stats.Consumed != 1 || stats.Revoked != 1 || stats.Total != 4 {
		t.Errorf("stats = %+v, want {1,1,1,1,4}", stats)
	}
}

func TestStore_ListBySubject(t *testing.T) {
	if testing.Short() || testDB == nil {
		t.Skip("integration test — Docker required")
	}
	ctx := context.Background()
	if _, err := testDB.Exec(ctx, "TRUNCATE auth_refresh_tokens"); err != nil {
		t.Fatal(err)
	}
	s := New(testDB)
	for i, seed := range []byte{1, 2, 3} {
		r := makeRecord(seed, "u-list", "55555555-5555-5555-5555-55555555555"+string(rune('0'+seed)), "1.2.3.4", time.Hour)
		// stagger issued_at so DESC ordering is deterministic.
		r.IssuedAt = time.Now().Add(time.Duration(i) * time.Second).UTC().Truncate(time.Second)
		if err := s.Issue(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	// Other subject — must NOT appear.
	other := makeRecord(9, "u-other", "99999999-9999-9999-9999-999999999999", "1.2.3.4", time.Hour)
	if err := s.Issue(ctx, other); err != nil {
		t.Fatal(err)
	}

	rows, err := s.ListBySubject(ctx, "u-list")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	for _, r := range rows {
		if r.Subject != "u-list" {
			t.Errorf("subject = %q, want u-list", r.Subject)
		}
		if r.State != "active" {
			t.Errorf("state = %q, want active", r.State)
		}
		if r.IP != "1.2.3.4" {
			t.Errorf("ip = %q", r.IP)
		}
	}
	// DESC by issued_at — newest first
	if !rows[0].IssuedAt.After(rows[2].IssuedAt) {
		t.Errorf("order wrong: %v / %v", rows[0].IssuedAt, rows[2].IssuedAt)
	}
}

func TestStore_ListBySubject_EmptySubject(t *testing.T) {
	if testing.Short() || testDB == nil {
		t.Skip()
	}
	s := New(testDB)
	rows, err := s.ListBySubject(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Errorf("rows = %d, want 0", len(rows))
	}
}

func TestStore_RevokeByIP(t *testing.T) {
	if testing.Short() || testDB == nil {
		t.Skip("integration test — Docker required")
	}
	ctx := context.Background()
	if _, err := testDB.Exec(ctx, "TRUNCATE auth_refresh_tokens"); err != nil {
		t.Fatal(err)
	}
	s := New(testDB)
	// Two tokens on 203.0.113.5 (both should be revoked).
	for _, seed := range []byte{1, 2} {
		r := makeRecord(seed, "u-x", "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "203.0.113.5", time.Hour)
		r.FamilyID = "00000000-0000-0000-0000-00000000000" + string(rune('0'+seed))
		if err := s.Issue(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	// One token on a different IP (must stay active).
	keep := makeRecord(9, "u-x", "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", "203.0.113.10", time.Hour)
	if err := s.Issue(ctx, keep); err != nil {
		t.Fatal(err)
	}

	n, err := s.RevokeByIP(ctx, "203.0.113.5")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("revoked = %d, want 2", n)
	}
	// Idempotent — second call revokes nothing.
	n2, err := s.RevokeByIP(ctx, "203.0.113.5")
	if err != nil {
		t.Fatal(err)
	}
	if n2 != 0 {
		t.Errorf("second call = %d, want 0", n2)
	}
	// Unknown IP is not an error.
	if got, err := s.RevokeByIP(ctx, "203.0.113.99"); err != nil || got != 0 {
		t.Errorf("unknown ip: got=%d err=%v", got, err)
	}
	// Empty IP short-circuits.
	if got, err := s.RevokeByIP(ctx, ""); err != nil || got != 0 {
		t.Errorf("empty ip: got=%d err=%v", got, err)
	}
	// The keep-token must still be active.
	stats, _ := s.Stats(ctx)
	if stats.Active != 1 || stats.Revoked != 2 {
		t.Errorf("stats = %+v, want active=1 revoked=2", stats)
	}
}

func TestStore_GarbageCollectBatch(t *testing.T) {
	if testing.Short() || testDB == nil {
		t.Skip("integration test — Docker required")
	}
	ctx := context.Background()
	if _, err := testDB.Exec(ctx, "TRUNCATE auth_refresh_tokens"); err != nil {
		t.Fatal(err)
	}
	s := New(testDB)
	// 5 expired records.
	for i := byte(1); i <= 5; i++ {
		r := makeRecord(i, "u-gc", "cccccccc-cccc-cccc-cccc-cccccccccccc", "127.0.0.1", time.Hour)
		r.FamilyID = "cccccccc-cccc-cccc-cccc-cccccccccc0" + string(rune('0'+i))
		r.ExpiresAt = time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
		if err := s.Issue(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	// 1 active record (must NOT be deleted).
	keep := makeRecord(9, "u-gc", "dddddddd-dddd-dddd-dddd-dddddddddddd", "127.0.0.1", time.Hour)
	if err := s.Issue(ctx, keep); err != nil {
		t.Fatal(err)
	}
	n, err := s.GarbageCollectBatch(ctx, time.Now(), 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if n != 5 {
		t.Errorf("deleted = %d, want 5", n)
	}
	stats, _ := s.Stats(ctx)
	if stats.Total != 1 || stats.Active != 1 {
		t.Errorf("stats = %+v, want total=1 active=1", stats)
	}
}

func TestStore_Metrics_OpsIncrement(t *testing.T) {
	if testing.Short() || testDB == nil {
		t.Skip("integration test — Docker required")
	}
	ctx := context.Background()
	if _, err := testDB.Exec(ctx, "TRUNCATE auth_refresh_tokens"); err != nil {
		t.Fatal(err)
	}
	reg := prometheus.NewRegistry()
	s := New(testDB, WithMetrics(reg))
	rec := makeRecord(1, "u-m", "eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee", "10.10.10.10", time.Hour)
	if err := s.Issue(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if got := counterValue(t, reg, "refreshpg_ops_total", "issue", "ok"); got != 1 {
		t.Errorf("issue/ok = %v, want 1", got)
	}
	if _, err := s.Consume(ctx, rec.TokenHash, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := counterValue(t, reg, "refreshpg_ops_total", "consume", "ok"); got != 1 {
		t.Errorf("consume/ok = %v, want 1", got)
	}
	// Histogram registered + observed.
	mfs, _ := reg.Gather()
	var found bool
	for _, mf := range mfs {
		if mf.GetName() == "refreshpg_op_duration_seconds" {
			found = true
		}
	}
	if !found {
		t.Error("refreshpg_op_duration_seconds not registered")
	}
}

func TestStore_OnConsumeReused_FiresOnReuse(t *testing.T) {
	if testing.Short() || testDB == nil {
		t.Skip("integration test — Docker required")
	}
	ctx := context.Background()
	if _, err := testDB.Exec(ctx, "TRUNCATE auth_refresh_tokens"); err != nil {
		t.Fatal(err)
	}
	var (
		fires   atomic.Int32
		gotFam  string
		gotSubj string
	)
	s := New(testDB, WithOnConsumeReused(func(_ context.Context, family, subject string) {
		fires.Add(1)
		gotFam = family
		gotSubj = subject
	}))
	rec := makeRecord(1, "u-r", "ffffffff-ffff-ffff-ffff-ffffffffffff", "10.10.10.10", time.Hour)
	if err := s.Issue(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Consume(ctx, rec.TokenHash, time.Now()); err != nil {
		t.Fatal(err)
	}
	// Second Consume on the same hash → reuse detection.
	_, err := s.Consume(ctx, rec.TokenHash, time.Now())
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != auth.CodeRefreshReused {
		t.Fatalf("err = %v, want CodeRefreshReused", err)
	}
	if fires.Load() != 1 {
		t.Errorf("hook fires = %d, want 1", fires.Load())
	}
	if gotFam != rec.FamilyID || gotSubj != "u-r" {
		t.Errorf("hook payload: family=%q subject=%q want %q/u-r", gotFam, gotSubj, rec.FamilyID)
	}
}

func TestStore_Hooks_PanicRecovered(t *testing.T) {
	if testing.Short() || testDB == nil {
		t.Skip("integration test — Docker required")
	}
	ctx := context.Background()
	if _, err := testDB.Exec(ctx, "TRUNCATE auth_refresh_tokens"); err != nil {
		t.Fatal(err)
	}
	logbuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logbuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	s := New(testDB,
		WithLogger(logger),
		WithOnFamilyRevoke(func(context.Context, string, int64) { panic("hook boom") }),
	)
	rec := makeRecord(1, "u-p", "abababab-abab-abab-abab-abababababab", "10.10.10.10", time.Hour)
	if err := s.Issue(ctx, rec); err != nil {
		t.Fatal(err)
	}
	// Must not panic.
	if err := s.RevokeFamily(ctx, rec.FamilyID); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(logbuf.Bytes(), []byte("OnFamilyRevoke panic recovered")) {
		t.Errorf("logger did not record panic; logs=%q", logbuf.String())
	}
}
