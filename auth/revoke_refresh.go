package auth

import (
	"context"
	"errors"

	xerrs "github.com/theizzatbek/gokit/errs"
)

// RevokeRefresh revokes the presented refresh token — the pure (Fiber-free)
// logout primitive for services that carry the token outside the kit's
// cookie (JSON body, RPC metadata, CLI tooling). Contractually idempotent:
// an unknown, malformed, expired or already-revoked token is NOT an error
// (nil) — logout must not distinguish "never existed" from "already
// revoked", and callers should never have to swallow errors. A non-nil
// error means a transient store failure only.
//
// No new tokens are issued. On TokenRevoker stores no other member of the
// token's family is touched; on fallback stores (no TokenRevoker),
// presenting an already-consumed token trips the store's reuse detection,
// which revokes the whole family — the fail-safe direction. For "log out
// this session on every device" use RevokeFamily.
func (a *Auth[C]) RevokeRefresh(ctx context.Context, refreshRaw string) error {
	if a.store == nil {
		return xerrs.Internal("store_unset", "auth: WithRefreshStore option was not provided")
	}
	if _, _, err := a.revokeByRaw(ctx, refreshRaw, false); err != nil {
		return err
	}
	a.metrics.incLogout("token")
	return nil
}

// RevokeFamily revokes the presented refresh token AND every other member
// of its rotation family ("log out on all devices holding this session").
// Same idempotency contract as RevokeRefresh.
func (a *Auth[C]) RevokeFamily(ctx context.Context, refreshRaw string) error {
	if a.store == nil {
		return xerrs.Internal("store_unset", "auth: WithRefreshStore option was not provided")
	}
	if _, _, err := a.revokeByRaw(ctx, refreshRaw, true); err != nil {
		return err
	}
	a.metrics.incLogout("family")
	return nil
}

// revokeByRaw is the shared engine behind RevokeRefresh / RevokeFamily and
// the Fiber Logout handler. found=true means the store located a record for
// the presented token (callers use rec for security logging). All benign
// conditions — unknown, expired, already consumed/revoked — collapse to
// found=false with a nil error; a non-nil error is a transient store
// failure. The caller checks a.store != nil.
func (a *Auth[C]) revokeByRaw(ctx context.Context, raw string, wholeFamily bool) (Record, bool, error) {
	if raw == "" {
		return Record{}, false, nil
	}
	hash := hashRefresh(raw)
	now := a.now()

	if tr, ok := a.store.(TokenRevoker); ok {
		rec, found, err := tr.RevokeToken(ctx, hash, now)
		if err != nil {
			return Record{}, false, xerrs.Wrap(err, xerrs.KindUnavailable, CodeStoreUnavailable, "refresh store unavailable")
		}
		if found && wholeFamily && rec.FamilyID != "" {
			if err := a.store.RevokeFamily(ctx, rec.FamilyID); err != nil {
				return rec, true, xerrs.Wrap(err, xerrs.KindUnavailable, CodeStoreUnavailable, "refresh store unavailable")
			}
		}
		return rec, found, nil
	}

	// Fallback for third-party stores that predate TokenRevoker: a Consume
	// with the would-be replacement pair never issued. The record ends up
	// consumed rather than revoked — re-presenting it still trips
	// reuse-detection, so the caller-visible contract holds. On the reused
	// branch the store has already revoked the whole family itself.
	rec, err := a.store.Consume(ctx, hash, now)
	if err != nil {
		var e *xerrs.Error
		if errors.As(err, &e) {
			switch e.Code {
			case CodeRefreshReused, CodeRefreshExpired, CodeRefreshInvalid:
				return Record{}, false, nil
			}
		}
		return Record{}, false, err
	}
	if wholeFamily && rec.FamilyID != "" {
		if err := a.store.RevokeFamily(ctx, rec.FamilyID); err != nil {
			return rec, true, xerrs.Wrap(err, xerrs.KindUnavailable, CodeStoreUnavailable, "refresh store unavailable")
		}
	}
	return rec, true, nil
}

// RevokeAllForSubject revokes every live refresh token belonging to subject
// — the administrative "log this user out everywhere" primitive (operator
// deactivation, credential-compromise response). Pure (Fiber-free)
// counterpart of the LogoutAll handler. Idempotent: a subject with no live
// tokens is nil; an empty subject is a no-op nil.
func (a *Auth[C]) RevokeAllForSubject(ctx context.Context, subject string) error {
	if a.store == nil {
		return xerrs.Internal("store_unset", "auth: WithRefreshStore option was not provided")
	}
	if subject == "" {
		return nil
	}
	if err := a.store.RevokeSubject(ctx, subject); err != nil {
		return xerrs.Wrap(err, xerrs.KindUnavailable, CodeStoreUnavailable, "refresh store unavailable")
	}
	a.metrics.incLogout("all")
	return nil
}
