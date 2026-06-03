package auth

import (
	"context"
	"sync"
	"time"
)

// RevokedAccessStore is the optional blacklist consulted by Bearer
// after JWT verification. Implementations key on `JTI` (claim `jti`)
// and report whether the token has been administratively revoked.
//
// Use to invalidate an access token before its natural `exp` — typical
// trigger is "credential compromise reported" or "manual session
// termination from admin UI". Refresh-token revocation already exists
// (RefreshStore.RevokeFamily / RevokeSubject); this covers the gap
// where the still-valid access token in the user's browser shouldn't
// outlive the incident.
//
// Backend choice is a tradeoff. Redis is the typical home: `SET jti
// "" PXAT <exp_millis>` makes the entry self-evicting at the same
// instant the JWT expires, so the blacklist stays bounded. Postgres
// works too — wire a periodic GC over `exp_at < now()`.
//
// All methods MUST honour the supplied ctx; IsRevoked is on the hot
// path so a transport timeout there is preferable to a hang. A non-nil
// error from IsRevoked is fail-OPEN by default (token is accepted) so
// a transient backend outage does not lock out every user — the
// failure is logged via Auth's security log instead.
type RevokedAccessStore interface {
	// IsRevoked returns true when jti has been administratively
	// revoked. A nil error + false means "no revocation found". A
	// non-nil error is logged at WARN on the security logger; the
	// request still proceeds.
	IsRevoked(ctx context.Context, jti string) (bool, error)

	// Revoke records jti as revoked until exp. exp is the JWT's `exp`
	// claim — backends MAY drop the row once now() > exp because the
	// JWT engine refuses tokens past exp regardless.
	Revoke(ctx context.Context, jti string, exp time.Time) error
}

// WithRevokedAccessStore wires an access-token blacklist into Auth.
// Bearer middleware consults the store after JWT verify succeeds and
// before populating Principal. Without this option the kit never
// performs a per-request blacklist lookup (zero hot-path cost).
//
// To revoke a token administratively, call Auth.RevokeAccess from
// your admin handler; the kit reads `JTI` and `ExpiresAt` from the
// supplied claims to store the right key + TTL.
func WithRevokedAccessStore(s RevokedAccessStore) Option {
	return func(o *options) { o.revokedAccess = s }
}

// RevokeAccess records `c.JTI` as revoked through the wired
// [RevokedAccessStore]. No-op when no store is wired (returns nil so
// callers can chain without checking the option).
//
// Returns *errs.Error from the store wrapped at the boundary so admin
// handlers can surface the failure to the operator.
func (a *Auth[C]) RevokeAccess(ctx context.Context, c Claims[C]) error {
	if a.revokedAccess == nil || c.JTI == "" {
		return nil
	}
	return a.revokedAccess.Revoke(ctx, c.JTI, time.Unix(c.ExpiresAt, 0))
}

// MemRevokedAccessStore is the in-process default — sync.Map backed,
// lazy eviction on read. Suitable for single-instance services. For
// multi-replica deployments, wire a Redis-backed store implementing the
// same interface.
type MemRevokedAccessStore struct {
	mu    sync.Mutex
	items map[string]time.Time
	now   func() time.Time
}

// NewMemRevokedAccessStore returns an empty in-process store.
func NewMemRevokedAccessStore() *MemRevokedAccessStore {
	return &MemRevokedAccessStore{items: map[string]time.Time{}, now: time.Now}
}

// IsRevoked reports whether jti is in the blacklist and not yet expired.
func (s *MemRevokedAccessStore) IsRevoked(_ context.Context, jti string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.items[jti]
	if !ok {
		return false, nil
	}
	if s.now().After(exp) {
		delete(s.items, jti)
		return false, nil
	}
	return true, nil
}

// Revoke records jti until exp. Past-exp entries are silently dropped.
func (s *MemRevokedAccessStore) Revoke(_ context.Context, jti string, exp time.Time) error {
	if jti == "" {
		return nil
	}
	if exp.IsZero() || exp.Before(s.now()) {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items[jti] = exp
	return nil
}
