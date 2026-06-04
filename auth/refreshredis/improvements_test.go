package refreshredis

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
			lm := labelMap(m)
			if lm["op"] == op && (outcome == "" || lm["outcome"] == outcome) {
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
		{"consumed", SessionInfo{ConsumedAt: now, ExpiresAt: now.Add(time.Hour)}, "consumed"},
		{"revoked-wins", SessionInfo{ConsumedAt: now, RevokedAt: now, ExpiresAt: now.Add(time.Hour)}, "revoked"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sessionState(tc.info, now); got != tc.want {
				t.Errorf("state = %q, want %q", got, tc.want)
			}
		})
	}
}

func makeRedisRecord(seed byte, subject, family, ip string, ttl time.Duration) auth.Record {
	var h [32]byte
	h[0] = seed
	now := time.Now().UTC().Truncate(time.Second)
	return auth.Record{
		TokenHash: h, Subject: subject, FamilyID: family,
		IssuedAt: now, ExpiresAt: now.Add(ttl),
		UserAgent: "ua-" + subject, IP: ip,
	}
}

func TestStore_Stats_RedisBuckets(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test — Docker required")
	}
	s := newStore(t)
	ctx := context.Background()
	// active
	if err := s.Issue(ctx, makeRedisRecord(1, "u-a", "f-a", "10.0.0.1", time.Hour)); err != nil {
		t.Fatal(err)
	}
	// consumed
	con := makeRedisRecord(2, "u-b", "f-b", "10.0.0.2", time.Hour)
	if err := s.Issue(ctx, con); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Consume(ctx, con.TokenHash, time.Now()); err != nil {
		t.Fatal(err)
	}
	// revoked
	rev := makeRedisRecord(3, "u-c", "f-c", "10.0.0.3", time.Hour)
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
	if stats.Active != 1 || stats.Consumed != 1 || stats.Revoked != 1 {
		t.Errorf("stats = %+v, want active=1 consumed=1 revoked=1", stats)
	}
	if stats.Total != 3 {
		t.Errorf("total = %d, want 3", stats.Total)
	}
}

func TestStore_ListBySubject_RedisOrdersDesc(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test — Docker required")
	}
	s := newStore(t)
	ctx := context.Background()
	for i, seed := range []byte{1, 2, 3} {
		r := makeRedisRecord(seed, "u-list", "f-"+string(rune('0'+seed)), "1.2.3.4", time.Hour)
		r.IssuedAt = time.Now().Add(time.Duration(i) * time.Second).UTC().Truncate(time.Second)
		if err := s.Issue(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.ListBySubject(ctx, "u-list")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	if !rows[0].IssuedAt.After(rows[2].IssuedAt) {
		t.Errorf("order: %v / %v", rows[0].IssuedAt, rows[2].IssuedAt)
	}
	for _, r := range rows {
		if r.Subject != "u-list" || r.State != "active" {
			t.Errorf("row = %+v", r)
		}
	}
}

func TestStore_ListBySubject_RedisEmpty(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	s := newStore(t)
	rows, err := s.ListBySubject(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Errorf("rows = %d, want 0", len(rows))
	}
}

func TestStore_RevokeByIP_Redis(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test — Docker required")
	}
	s := newStore(t)
	ctx := context.Background()
	for _, seed := range []byte{1, 2} {
		r := makeRedisRecord(seed, "u-x", "f-x-"+string(rune('0'+seed)), "203.0.113.5", time.Hour)
		if err := s.Issue(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	keep := makeRedisRecord(9, "u-x", "f-x-keep", "203.0.113.10", time.Hour)
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
	// keep token must remain active.
	stats, _ := s.Stats(ctx)
	if stats.Active != 1 || stats.Revoked != 2 {
		t.Errorf("stats = %+v, want active=1 revoked=2", stats)
	}
	// Empty IP short-circuits.
	if got, err := s.RevokeByIP(ctx, ""); err != nil || got != 0 {
		t.Errorf("empty ip: got=%d err=%v", got, err)
	}
}

func TestStore_Metrics_OpsIncrement_Redis(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test — Docker required")
	}
	if testRedis == nil {
		t.Skip("redis not available")
	}
	if err := testRedis.FlushAll(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	reg := prometheus.NewRegistry()
	s := New(testRedis, WithMetrics(reg))
	rec := makeRedisRecord(1, "u-m", "f-m", "10.10.10.10", time.Hour)
	if err := s.Issue(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	if got := counterValue(t, reg, "refreshredis_ops_total", "issue", "ok"); got != 1 {
		t.Errorf("issue/ok = %v, want 1", got)
	}
	if _, err := s.Consume(context.Background(), rec.TokenHash, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := counterValue(t, reg, "refreshredis_ops_total", "consume", "ok"); got != 1 {
		t.Errorf("consume/ok = %v, want 1", got)
	}
}

func TestStore_OnConsumeReused_Fires_Redis(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test — Docker required")
	}
	if testRedis == nil {
		t.Skip()
	}
	if err := testRedis.FlushAll(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	var fires atomic.Int32
	var gotFam, gotSubj string
	s := New(testRedis, WithOnConsumeReused(func(_ context.Context, family, subject string) {
		fires.Add(1)
		gotFam = family
		gotSubj = subject
	}))
	rec := makeRedisRecord(1, "u-r", "f-r", "10.10.10.10", time.Hour)
	if err := s.Issue(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Consume(context.Background(), rec.TokenHash, time.Now()); err != nil {
		t.Fatal(err)
	}
	_, err := s.Consume(context.Background(), rec.TokenHash, time.Now())
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != auth.CodeRefreshReused {
		t.Fatalf("err = %v, want CodeRefreshReused", err)
	}
	if fires.Load() != 1 {
		t.Errorf("hook fires = %d, want 1", fires.Load())
	}
	if gotFam != "f-r" || gotSubj != "u-r" {
		t.Errorf("hook payload: family=%q subject=%q", gotFam, gotSubj)
	}
}

func TestStore_Hooks_PanicRecovered_Redis(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test — Docker required")
	}
	if testRedis == nil {
		t.Skip()
	}
	if err := testRedis.FlushAll(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	logbuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logbuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	s := New(testRedis,
		WithLogger(logger),
		WithOnFamilyRevoke(func(context.Context, string, int64) { panic("boom") }),
	)
	rec := makeRedisRecord(1, "u-p", "f-p", "10.10.10.10", time.Hour)
	if err := s.Issue(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeFamily(context.Background(), rec.FamilyID); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(logbuf.Bytes(), []byte("OnFamilyRevoke panic recovered")) {
		t.Errorf("logger missing panic record; logs=%q", logbuf.String())
	}
}
