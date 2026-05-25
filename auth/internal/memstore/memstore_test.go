package memstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/theizzatbek/gokit/auth"
	"github.com/theizzatbek/gokit/errs"
)

func TestIssueThenConsume(t *testing.T) {
	s := New()
	ctx := context.Background()
	now := time.Now()
	var h [32]byte
	h[0] = 1
	rec := auth.Record{
		TokenHash: h, Subject: "u", FamilyID: "f1",
		IssuedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	if err := s.Issue(ctx, rec); err != nil {
		t.Fatalf("issue: %v", err)
	}
	got, err := s.Consume(ctx, h, now)
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if got.Subject != "u" {
		t.Fatalf("subject = %q", got.Subject)
	}
}

func TestConsume_NotFound(t *testing.T) {
	s := New()
	_, err := s.Consume(context.Background(), [32]byte{9}, time.Now())
	assertCode(t, err, auth.CodeRefreshInvalid)
}

func TestConsume_Expired(t *testing.T) {
	s := New()
	now := time.Now()
	var h [32]byte
	h[0] = 1
	_ = s.Issue(context.Background(), auth.Record{TokenHash: h, ExpiresAt: now.Add(-time.Second), FamilyID: "f"})
	_, err := s.Consume(context.Background(), h, now)
	assertCode(t, err, auth.CodeRefreshExpired)
}

func TestConsume_Reused_RevokesFamily(t *testing.T) {
	s := New()
	ctx := context.Background()
	now := time.Now()
	var h1, h2 [32]byte
	h1[0] = 1
	h2[0] = 2
	_ = s.Issue(ctx, auth.Record{TokenHash: h1, FamilyID: "f", ExpiresAt: now.Add(time.Hour)})
	_ = s.Issue(ctx, auth.Record{TokenHash: h2, FamilyID: "f", ExpiresAt: now.Add(time.Hour)})
	if _, err := s.Consume(ctx, h1, now); err != nil {
		t.Fatalf("first consume: %v", err)
	}
	// h1 is now consumed; presenting it again must revoke the whole family
	// (which includes h2 even though it's untouched).
	_, err := s.Consume(ctx, h1, now)
	assertCode(t, err, auth.CodeRefreshReused)
	_, err2 := s.Consume(ctx, h2, now)
	assertCode(t, err2, auth.CodeRefreshReused) // family revoked
}

func TestRevokeSubject(t *testing.T) {
	s := New()
	ctx := context.Background()
	now := time.Now()
	var h [32]byte
	h[0] = 1
	_ = s.Issue(ctx, auth.Record{TokenHash: h, Subject: "u-1", FamilyID: "f", ExpiresAt: now.Add(time.Hour)})
	if err := s.RevokeSubject(ctx, "u-1"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	_, err := s.Consume(ctx, h, now)
	assertCode(t, err, auth.CodeRefreshReused)
}

func TestGarbageCollect(t *testing.T) {
	s := New()
	ctx := context.Background()
	now := time.Now()
	var live, dead [32]byte
	live[0] = 1
	dead[0] = 2
	_ = s.Issue(ctx, auth.Record{TokenHash: live, ExpiresAt: now.Add(time.Hour), FamilyID: "f1"})
	_ = s.Issue(ctx, auth.Record{TokenHash: dead, ExpiresAt: now.Add(-time.Hour), FamilyID: "f2"})
	n, err := s.GarbageCollect(ctx, now)
	if err != nil {
		t.Fatalf("gc: %v", err)
	}
	if n != 1 {
		t.Fatalf("gc removed %d, want 1", n)
	}
}

func assertCode(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error code %q, got nil", want)
	}
	var e *errs.Error
	if !errors.As(err, &e) {
		t.Fatalf("err not *errs.Error: %v", err)
	}
	if e.Code != want {
		t.Fatalf("code = %q, want %q", e.Code, want)
	}
}
