package refreshredis

import (
	"context"
	"errors"
	"flag"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	"github.com/theizzatbek/gokit/auth"
	"github.com/theizzatbek/gokit/errs"
)

var testRedis *redis.Client

func TestMain(m *testing.M) {
	os.Exit(runMain(m))
}

func runMain(m *testing.M) int {
	flag.Parse()
	if testing.Short() {
		// Skip the entire package under -short — every test needs
		// the Redis container.
		return 0
	}
	ctx := context.Background()
	c, err := tcredis.Run(ctx, "redis:7-alpine")
	if err != nil {
		println("testcontainers redis start failed:", err.Error())
		return 1
	}
	defer func() {
		if termErr := testcontainers.TerminateContainer(c); termErr != nil {
			println("testcontainers terminate:", termErr.Error())
		}
	}()
	endpoint, err := c.Endpoint(ctx, "")
	if err != nil {
		println("redis endpoint:", err.Error())
		return 1
	}
	testRedis = redis.NewClient(&redis.Options{Addr: endpoint})
	defer testRedis.Close()
	if err := testRedis.Ping(ctx).Err(); err != nil {
		println("redis ping:", err.Error())
		return 1
	}
	return m.Run()
}

func newStore(t *testing.T) *Store {
	t.Helper()
	if testRedis == nil {
		t.Skip("integration test — Docker required")
	}
	if err := testRedis.FlushAll(context.Background()).Err(); err != nil {
		t.Fatalf("flushall: %v", err)
	}
	return New(testRedis)
}

func TestIssueAndDirectRead(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	ctx := context.Background()
	s := newStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	var h [32]byte
	h[0] = 0xAB
	rec := auth.Record{
		TokenHash: h, Subject: "u-1",
		FamilyID:  "00000000-0000-0000-0000-000000000001",
		IssuedAt:  now,
		ExpiresAt: now.Add(time.Hour),
	}
	if err := s.Issue(ctx, rec); err != nil {
		t.Fatalf("issue: %v", err)
	}
	if got := testRedis.Exists(ctx, refreshKey(h)).Val(); got != 1 {
		t.Fatalf("refresh key missing")
	}
	if got := testRedis.SIsMember(ctx, familyKey(rec.FamilyID), hashToHex(h)).Val(); !got {
		t.Fatalf("family set missing token")
	}
}

func TestConsume_RoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	ctx := context.Background()
	s := newStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	var h [32]byte
	h[0] = 1
	_ = s.Issue(ctx, auth.Record{TokenHash: h, Subject: "u", FamilyID: "11111111-1111-1111-1111-111111111111",
		IssuedAt: now, ExpiresAt: now.Add(time.Hour)})
	got, err := s.Consume(ctx, h, now)
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if got.Subject != "u" {
		t.Fatalf("subject = %q", got.Subject)
	}
}

func TestConsume_NotFound(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	s := newStore(t)
	_, err := s.Consume(context.Background(), [32]byte{0xFF}, time.Now())
	assertCode(t, err, auth.CodeRefreshInvalid)
}

func TestConsume_Expired(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	ctx := context.Background()
	s := newStore(t)
	now := time.Now().UTC()
	var h [32]byte
	h[0] = 1
	// Issue with already-expired expires_at — manually since Issue's EXPIREAT
	// would refuse a past instant; backdoor via raw HSet.
	_ = testRedis.HSet(ctx, refreshKey(h), map[string]any{
		"family": "22222222-2222-2222-2222-222222222222", "subject": "u",
		"issued_at":  now.Add(-2 * time.Hour).Unix(),
		"expires_at": now.Add(-time.Hour).Unix(),
		"consumed":   "0", "revoked": "0",
	}).Err()
	_, err := s.Consume(ctx, h, now)
	assertCode(t, err, auth.CodeRefreshExpired)
}

func TestConsume_ReusedRevokesFamily(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	ctx := context.Background()
	s := newStore(t)
	now := time.Now().UTC()
	fam := "33333333-3333-3333-3333-333333333333"
	var h1, h2 [32]byte
	h1[0] = 1
	h2[0] = 2
	_ = s.Issue(ctx, auth.Record{TokenHash: h1, FamilyID: fam, Subject: "u",
		IssuedAt: now, ExpiresAt: now.Add(time.Hour)})
	_ = s.Issue(ctx, auth.Record{TokenHash: h2, FamilyID: fam, Subject: "u",
		IssuedAt: now, ExpiresAt: now.Add(time.Hour)})
	if _, err := s.Consume(ctx, h1, now); err != nil {
		t.Fatalf("first: %v", err)
	}
	_, err := s.Consume(ctx, h1, now)
	assertCode(t, err, auth.CodeRefreshReused)
	_, err2 := s.Consume(ctx, h2, now)
	assertCode(t, err2, auth.CodeRefreshReused)
}

func TestRevokeFamily_Redis(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	ctx := context.Background()
	s := newStore(t)
	now := time.Now().UTC()
	fam := "44444444-4444-4444-4444-444444444444"
	var h [32]byte
	h[0] = 1
	_ = s.Issue(ctx, auth.Record{TokenHash: h, FamilyID: fam, Subject: "u", IssuedAt: now, ExpiresAt: now.Add(time.Hour)})
	if err := s.RevokeFamily(ctx, fam); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	_, err := s.Consume(ctx, h, now)
	assertCode(t, err, auth.CodeRefreshReused)
}

func TestRevokeSubject_Redis(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	ctx := context.Background()
	s := newStore(t)
	now := time.Now().UTC()
	var h [32]byte
	h[0] = 1
	_ = s.Issue(ctx, auth.Record{TokenHash: h, Subject: "u-1",
		FamilyID: "55555555-5555-5555-5555-555555555555",
		IssuedAt: now, ExpiresAt: now.Add(time.Hour)})
	if err := s.RevokeSubject(ctx, "u-1"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	_, err := s.Consume(ctx, h, now)
	assertCode(t, err, auth.CodeRefreshReused)
}

func TestGarbageCollect_Redis(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	// Redis EXPIREAT already removes expired keys — GarbageCollect just reports
	// a best-effort count by scanning the family/subject sets for stale entries.
	ctx := context.Background()
	s := newStore(t)
	if _, err := s.GarbageCollect(ctx, time.Now()); err != nil {
		t.Fatalf("gc: %v", err)
	}
}

func assertCode(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected code %q, got nil", want)
	}
	var e *errs.Error
	if !errors.As(err, &e) {
		t.Fatalf("err type = %T", err)
	}
	if e.Code != want {
		t.Fatalf("code = %q, want %q", e.Code, want)
	}
}
