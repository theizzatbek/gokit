package auth_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/theizzatbek/gokit/auth"
	"github.com/theizzatbek/gokit/auth/internal/memstore"
	"github.com/theizzatbek/gokit/errs"
)

// newRevokeAuth builds an Auth over a memstore it also returns, so tests can
// inspect record state after revocation.
func newRevokeAuth(t *testing.T) (*auth.Auth[appClaims], *memstore.Mem) {
	t.Helper()
	keys, err := auth.GenerateEd25519Key("k1")
	if err != nil {
		t.Fatalf("keys: %v", err)
	}
	store := memstore.New()
	a, err := auth.New[appClaims](auth.Config{
		Issuer: "myapp", Audience: []string{"web"},
		Keys: keys, AccessTTL: 15 * time.Minute, RefreshTTL: 30 * 24 * time.Hour,
	}, auth.WithRefreshStore(store), auth.WithCookieSecure(false))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a, store
}

// rtHash mirrors the kit's internal raw->hash derivation (documented as
// plain SHA-256 over the wire form).
func rtHash(raw string) [32]byte { return sha256.Sum256([]byte(raw)) }

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("err = %v, want *errs.Error{Code:%q}", err, code)
	}
}

func TestRevokeRefresh_RevokesPresentedTokenOnly(t *testing.T) {
	a, store := newRevokeAuth(t)
	ctx := context.Background()

	pair1, err := a.IssueTokens(ctx, auth.LoginResult[appClaims]{Subject: "u-1"}, auth.IssueMeta{})
	if err != nil {
		t.Fatalf("IssueTokens: %v", err)
	}
	pair2, err := a.RotateRefresh(ctx, pair1.RefreshRaw, auth.IssueMeta{})
	if err != nil {
		t.Fatalf("RotateRefresh: %v", err)
	}

	if err := a.RevokeRefresh(ctx, pair2.RefreshRaw); err != nil {
		t.Fatalf("RevokeRefresh: %v", err)
	}

	rec2, ok := store.Get(rtHash(pair2.RefreshRaw))
	if !ok || rec2.RevokedAt == nil {
		t.Fatalf("presented token not revoked: ok=%v rec=%+v", ok, rec2)
	}
	// Single-token scope: the consumed ancestor is NOT revoked.
	rec1, ok := store.Get(rtHash(pair1.RefreshRaw))
	if !ok || rec1.RevokedAt != nil {
		t.Fatalf("ancestor unexpectedly revoked: ok=%v rec=%+v", ok, rec1)
	}
	// Re-presentation behaves exactly like reuse-detection.
	_, err = a.RotateRefresh(ctx, pair2.RefreshRaw, auth.IssueMeta{})
	wantCode(t, err, auth.CodeRefreshReused)
}

func TestRevokeRefresh_Idempotent(t *testing.T) {
	a, _ := newRevokeAuth(t)
	ctx := context.Background()
	pair, err := a.IssueTokens(ctx, auth.LoginResult[appClaims]{Subject: "u-1"}, auth.IssueMeta{})
	if err != nil {
		t.Fatalf("IssueTokens: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := a.RevokeRefresh(ctx, pair.RefreshRaw); err != nil {
			t.Fatalf("RevokeRefresh call %d: %v", i+1, err)
		}
	}
}

func TestRevokeRefresh_UnknownEmptyAndGarbage(t *testing.T) {
	a, _ := newRevokeAuth(t)
	ctx := context.Background()
	for _, raw := range []string{"", "rt_garbage", "not-even-a-token"} {
		if err := a.RevokeRefresh(ctx, raw); err != nil {
			t.Fatalf("RevokeRefresh(%q) = %v, want nil", raw, err)
		}
		if err := a.RevokeFamily(ctx, raw); err != nil {
			t.Fatalf("RevokeFamily(%q) = %v, want nil", raw, err)
		}
	}
}

func TestRevokeRefresh_OtherSessionsUnaffected(t *testing.T) {
	a, _ := newRevokeAuth(t)
	ctx := context.Background()
	pairA, _ := a.IssueTokens(ctx, auth.LoginResult[appClaims]{Subject: "u-1"}, auth.IssueMeta{})
	pairB, _ := a.IssueTokens(ctx, auth.LoginResult[appClaims]{Subject: "u-1"}, auth.IssueMeta{})

	if err := a.RevokeRefresh(ctx, pairA.RefreshRaw); err != nil {
		t.Fatalf("RevokeRefresh: %v", err)
	}
	if _, err := a.RotateRefresh(ctx, pairB.RefreshRaw, auth.IssueMeta{}); err != nil {
		t.Fatalf("second session broken by revoke of first: %v", err)
	}
}

func TestRevokeRefresh_StoreUnset(t *testing.T) {
	keys, _ := auth.GenerateEd25519Key("k1")
	a, err := auth.New[appClaims](auth.Config{
		Issuer: "myapp", Keys: keys,
		AccessTTL: time.Minute, RefreshTTL: time.Hour,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	wantCode(t, a.RevokeRefresh(context.Background(), "rt_x"), "store_unset")
	wantCode(t, a.RevokeFamily(context.Background(), "rt_x"), "store_unset")
}

func TestRevokeFamily_RevokesWholeChain(t *testing.T) {
	a, store := newRevokeAuth(t)
	ctx := context.Background()
	pair1, _ := a.IssueTokens(ctx, auth.LoginResult[appClaims]{Subject: "u-1"}, auth.IssueMeta{})
	pair2, err := a.RotateRefresh(ctx, pair1.RefreshRaw, auth.IssueMeta{})
	if err != nil {
		t.Fatalf("RotateRefresh: %v", err)
	}

	if err := a.RevokeFamily(ctx, pair2.RefreshRaw); err != nil {
		t.Fatalf("RevokeFamily: %v", err)
	}
	for name, raw := range map[string]string{"presented": pair2.RefreshRaw, "ancestor": pair1.RefreshRaw} {
		rec, ok := store.Get(rtHash(raw))
		if !ok || rec.RevokedAt == nil {
			t.Fatalf("%s member not revoked: ok=%v rec=%+v", name, ok, rec)
		}
	}
	// Idempotent second call.
	if err := a.RevokeFamily(ctx, pair2.RefreshRaw); err != nil {
		t.Fatalf("second RevokeFamily: %v", err)
	}
	_, err = a.RotateRefresh(ctx, pair2.RefreshRaw, auth.IssueMeta{})
	wantCode(t, err, auth.CodeRefreshReused)
}

// plainStore hides memstore's TokenRevoker so tests exercise the
// Consume-based fallback path in revokeByRaw.
type plainStore struct{ inner *memstore.Mem }

func (p plainStore) Issue(ctx context.Context, r auth.Record) error { return p.inner.Issue(ctx, r) }
func (p plainStore) Consume(ctx context.Context, h [32]byte, now time.Time) (auth.Record, error) {
	return p.inner.Consume(ctx, h, now)
}
func (p plainStore) RevokeFamily(ctx context.Context, f string) error {
	return p.inner.RevokeFamily(ctx, f)
}
func (p plainStore) RevokeSubject(ctx context.Context, s string) error {
	return p.inner.RevokeSubject(ctx, s)
}
func (p plainStore) GarbageCollect(ctx context.Context, now time.Time) (int64, error) {
	return p.inner.GarbageCollect(ctx, now)
}

func newFallbackAuth(t *testing.T) (*auth.Auth[appClaims], *memstore.Mem) {
	t.Helper()
	keys, err := auth.GenerateEd25519Key("k1")
	if err != nil {
		t.Fatalf("keys: %v", err)
	}
	inner := memstore.New()
	a, err := auth.New[appClaims](auth.Config{
		Issuer: "myapp", Keys: keys,
		AccessTTL: time.Minute, RefreshTTL: time.Hour,
	}, auth.WithRefreshStore(plainStore{inner: inner}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a, inner
}

func TestRevokeRefresh_FallbackConsumes(t *testing.T) {
	a, inner := newFallbackAuth(t)
	ctx := context.Background()
	pair, _ := a.IssueTokens(ctx, auth.LoginResult[appClaims]{Subject: "u-1"}, auth.IssueMeta{})

	if err := a.RevokeRefresh(ctx, pair.RefreshRaw); err != nil {
		t.Fatalf("RevokeRefresh (fallback): %v", err)
	}
	rec, ok := inner.Get(rtHash(pair.RefreshRaw))
	if !ok || (rec.ConsumedAt == nil && rec.RevokedAt == nil) {
		t.Fatalf("fallback left token live: ok=%v rec=%+v", ok, rec)
	}
	// Idempotent: the second call hits the reused branch — still nil.
	if err := a.RevokeRefresh(ctx, pair.RefreshRaw); err != nil {
		t.Fatalf("second RevokeRefresh (fallback): %v", err)
	}
	// Re-presentation trips reuse detection.
	_, err := a.RotateRefresh(ctx, pair.RefreshRaw, auth.IssueMeta{})
	wantCode(t, err, auth.CodeRefreshReused)
}

func TestRevokeFamily_FallbackRevokesChain(t *testing.T) {
	a, inner := newFallbackAuth(t)
	ctx := context.Background()
	pair1, _ := a.IssueTokens(ctx, auth.LoginResult[appClaims]{Subject: "u-1"}, auth.IssueMeta{})
	pair2, err := a.RotateRefresh(ctx, pair1.RefreshRaw, auth.IssueMeta{})
	if err != nil {
		t.Fatalf("RotateRefresh: %v", err)
	}

	if err := a.RevokeFamily(ctx, pair2.RefreshRaw); err != nil {
		t.Fatalf("RevokeFamily (fallback): %v", err)
	}
	rec1, _ := inner.Get(rtHash(pair1.RefreshRaw))
	if rec1.RevokedAt == nil {
		t.Fatalf("fallback family revoke missed ancestor: %+v", rec1)
	}
	if err := a.RevokeFamily(ctx, pair2.RefreshRaw); err != nil {
		t.Fatalf("second RevokeFamily (fallback): %v", err)
	}
}

// revokeFailStore fails RevokeToken with a transient error (primary path).
type revokeFailStore struct{ auth.RefreshStore }

func (revokeFailStore) RevokeToken(context.Context, [32]byte, time.Time) (auth.Record, bool, error) {
	return auth.Record{}, false, errs.Wrap(errors.New("conn refused"), errs.KindUnavailable, "store_down", "boom")
}

// consumeFailStore fails Consume with a transient error (fallback path).
type consumeFailStore struct{ auth.RefreshStore }

func (consumeFailStore) Consume(context.Context, [32]byte, time.Time) (auth.Record, error) {
	return auth.Record{}, errs.Wrap(errors.New("conn refused"), errs.KindUnavailable, "store_down", "boom")
}

func TestRevoke_TransientStoreErrorPropagates(t *testing.T) {
	keys, _ := auth.GenerateEd25519Key("k1")
	mk := func(store auth.RefreshStore) *auth.Auth[appClaims] {
		a, err := auth.New[appClaims](auth.Config{
			Issuer: "myapp", Keys: keys,
			AccessTTL: time.Minute, RefreshTTL: time.Hour,
		}, auth.WithRefreshStore(store))
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return a
	}
	ctx := context.Background()

	// Primary path: RevokeToken failure surfaces as store_unavailable.
	a := mk(revokeFailStore{RefreshStore: memstore.New()})
	wantCode(t, a.RevokeRefresh(ctx, "rt_x"), auth.CodeStoreUnavailable)
	wantCode(t, a.RevokeFamily(ctx, "rt_x"), auth.CodeStoreUnavailable)

	// Fallback path: a non-benign Consume error propagates unchanged.
	a = mk(consumeFailStore{RefreshStore: memstore.New()})
	wantCode(t, a.RevokeRefresh(ctx, "rt_x"), "store_down")
}

func TestRevokeAllForSubject_RevokesEverySession(t *testing.T) {
	a, store := newRevokeAuth(t)
	ctx := context.Background()
	pairA, _ := a.IssueTokens(ctx, auth.LoginResult[appClaims]{Subject: "u-1"}, auth.IssueMeta{})
	pairB, _ := a.IssueTokens(ctx, auth.LoginResult[appClaims]{Subject: "u-1"}, auth.IssueMeta{})
	pairOther, _ := a.IssueTokens(ctx, auth.LoginResult[appClaims]{Subject: "u-2"}, auth.IssueMeta{})

	if err := a.RevokeAllForSubject(ctx, "u-1"); err != nil {
		t.Fatalf("RevokeAllForSubject: %v", err)
	}
	for name, raw := range map[string]string{"A": pairA.RefreshRaw, "B": pairB.RefreshRaw} {
		rec, ok := store.Get(rtHash(raw))
		if !ok || rec.RevokedAt == nil {
			t.Fatalf("session %s not revoked: ok=%v rec=%+v", name, ok, rec)
		}
	}
	// Other subjects untouched.
	if _, err := a.RotateRefresh(ctx, pairOther.RefreshRaw, auth.IssueMeta{}); err != nil {
		t.Fatalf("u-2 session broken: %v", err)
	}
	// Idempotent + unknown subject + empty subject are all nil.
	if err := a.RevokeAllForSubject(ctx, "u-1"); err != nil {
		t.Fatalf("second call: %v", err)
	}
	if err := a.RevokeAllForSubject(ctx, "ghost"); err != nil {
		t.Fatalf("unknown subject: %v", err)
	}
	if err := a.RevokeAllForSubject(ctx, ""); err != nil {
		t.Fatalf("empty subject: %v", err)
	}
}

func TestRevokeAllForSubject_StoreUnset(t *testing.T) {
	keys, _ := auth.GenerateEd25519Key("k1")
	a, _ := auth.New[appClaims](auth.Config{
		Issuer: "myapp", Keys: keys,
		AccessTTL: time.Minute, RefreshTTL: time.Hour,
	})
	wantCode(t, a.RevokeAllForSubject(context.Background(), "u-1"), "store_unset")
}
