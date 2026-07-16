package refreshredis

import (
	"context"
	"testing"
	"time"

	"github.com/theizzatbek/gokit/auth"
)

func TestRevokeToken_LiveRecord(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	ctx := context.Background()
	s := newStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	var h [32]byte
	h[0] = 0xE1
	rec := auth.Record{
		TokenHash: h, Subject: "u-rt", FamilyID: "99999999-9999-9999-9999-999999999901",
		IssuedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	if err := s.Issue(ctx, rec); err != nil {
		t.Fatalf("issue: %v", err)
	}

	got, found, err := s.RevokeToken(ctx, h, now)
	if err != nil || !found {
		t.Fatalf("RevokeToken = (found=%v, err=%v), want (true, nil)", found, err)
	}
	if got.Subject != "u-rt" || got.FamilyID != rec.FamilyID {
		t.Fatalf("returned record mismatch: %+v", got)
	}
	if v := testRedis.HGet(ctx, refreshKey(h), "revoked").Val(); v != "1" {
		t.Fatalf("revoked flag = %q, want \"1\"", v)
	}

	// Idempotent second call.
	if _, found, err := s.RevokeToken(ctx, h, now); err != nil || !found {
		t.Fatalf("second RevokeToken = (found=%v, err=%v), want (true, nil)", found, err)
	}

	// Reuse-detection parity.
	_, err = s.Consume(ctx, h, now)
	assertCode(t, err, auth.CodeRefreshReused)
}

func TestRevokeToken_NotFound(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	s := newStore(t)
	rec, found, err := s.RevokeToken(context.Background(), [32]byte{0xE2}, time.Now())
	if err != nil || found {
		t.Fatalf("RevokeToken(unknown) = (%+v, %v, %v), want (zero, false, nil)", rec, found, err)
	}
}
