package refreshpg

import (
	"context"
	"testing"
	"time"

	"github.com/theizzatbek/gokit/auth"
)

func TestRevokeToken_LiveRecord(t *testing.T) {
	if testing.Short() || testDB == nil {
		t.Skip("integration test — Docker required")
	}
	ctx := context.Background()
	s := New(testDB)
	now := time.Now().UTC().Truncate(time.Second)
	var h [32]byte
	h[0], h[1] = 0xEE, 0x01
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

	var revokedAt *time.Time
	if err := testDB.QueryRow(ctx,
		"SELECT revoked_at FROM auth_refresh_tokens WHERE token_hash = $1", h[:]).Scan(&revokedAt); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if revokedAt == nil {
		t.Fatal("revoked_at not set")
	}
	first := *revokedAt

	// Idempotent: second call keeps the original revoked_at.
	if _, found, err := s.RevokeToken(ctx, h, now.Add(time.Minute)); err != nil || !found {
		t.Fatalf("second RevokeToken = (found=%v, err=%v), want (true, nil)", found, err)
	}
	if err := testDB.QueryRow(ctx,
		"SELECT revoked_at FROM auth_refresh_tokens WHERE token_hash = $1", h[:]).Scan(&revokedAt); err != nil {
		t.Fatalf("scan 2: %v", err)
	}
	if !revokedAt.Equal(first) {
		t.Fatalf("second revoke moved revoked_at: %v -> %v", first, *revokedAt)
	}

	// Reuse-detection parity: consuming a revoked token reports reused.
	if _, err := s.Consume(ctx, h, now); err == nil {
		t.Fatal("Consume after revoke succeeded, want refresh_reused")
	}
}

func TestRevokeToken_NotFound(t *testing.T) {
	if testing.Short() || testDB == nil {
		t.Skip("integration test — Docker required")
	}
	s := New(testDB)
	var h [32]byte
	h[0], h[1] = 0xEE, 0x02
	rec, found, err := s.RevokeToken(context.Background(), h, time.Now())
	if err != nil || found {
		t.Fatalf("RevokeToken(unknown) = (%+v, %v, %v), want (zero, false, nil)", rec, found, err)
	}
}

func TestRevokeToken_ConsumedRecordStillFound(t *testing.T) {
	if testing.Short() || testDB == nil {
		t.Skip("integration test — Docker required")
	}
	ctx := context.Background()
	s := New(testDB)
	now := time.Now().UTC().Truncate(time.Second)
	var h [32]byte
	h[0], h[1] = 0xEE, 0x03
	_ = s.Issue(ctx, auth.Record{
		TokenHash: h, Subject: "u-rt", FamilyID: "99999999-9999-9999-9999-999999999903",
		IssuedAt: now, ExpiresAt: now.Add(time.Hour),
	})
	if _, err := s.Consume(ctx, h, now); err != nil {
		t.Fatalf("consume: %v", err)
	}
	_, found, err := s.RevokeToken(ctx, h, now)
	if err != nil || !found {
		t.Fatalf("RevokeToken(consumed) = (found=%v, err=%v), want (true, nil)", found, err)
	}
}
