package auth

import (
	"context"
	"time"

	"github.com/google/uuid"

	xerrs "github.com/theizzatbek/gokit/errs"
)

// IssueMeta carries the per-request context kit handlers normally read from
// *fiber.Ctx. Pure callers (IssueTokens, RotateRefresh) pass it explicitly so
// they can be used outside Fiber — for example from RPC handlers, CLI tools,
// or custom HTTP frameworks. Both fields are optional; they are stored on
// Record purely for forensic / audit purposes.
type IssueMeta struct {
	UserAgent string
	IP        string
}

// TokenPair is the result of IssueTokens / RotateRefresh. Subject and the two
// expiry times are derived from the issued claims and the configured TTLs;
// AccessExpiresIn is provided as a convenience for clients that prefer
// duration-since-now (matches the JSON shape kit wrappers emit).
//
// RefreshRaw is the wire form of the refresh token. The kit stores only the
// SHA-256 hash; this raw value must be conveyed to the client (typically via
// HttpOnly cookie set by IssueLogin) — it is the only window in which the
// caller can read it.
type TokenPair struct {
	Access           string
	AccessExpiresAt  time.Time
	AccessExpiresIn  time.Duration
	RefreshRaw       string
	RefreshExpiresAt time.Time
	Subject          string
}

// IssueTokens is the pure (Fiber-free) issuance primitive. Build the
// LoginResult yourself — by parsing whatever wire format (password, PKCS7,
// mTLS, SSO assertion, magic link) — then call this to mint an access JWT,
// generate + persist a refresh token, and get both back.
//
// The caller is responsible for delivering the resulting TokenPair to the
// client (cookie, JSON body, gRPC trailers — your choice). IssueLogin wraps
// this with the default {access_token JSON + refresh HttpOnly cookie} shape.
//
// Returns *errs.Error{KindInternal, Code:"store_unset"} if no RefreshStore
// was configured; *errs.Error{KindUnavailable, Code:"store_unavailable"} on
// store.Issue failure.
func (a *Auth[C]) IssueTokens(ctx context.Context, res LoginResult[C], meta IssueMeta) (TokenPair, error) {
	if a.store == nil {
		return TokenPair{}, xerrs.Internal("store_unset", "auth: WithRefreshStore option was not provided")
	}

	now := a.now()
	accessExpiresAt := now.Add(a.accessTTL)
	refreshExpiresAt := now.Add(a.refreshTTL)

	claims := Claims[C]{
		Subject:   res.Subject,
		IssuedAt:  now.Unix(),
		ExpiresAt: accessExpiresAt.Unix(),
		JTI:       uuid.NewString(),
		Scopes:    res.Scopes,
		Roles:     res.Roles,
		Custom:    res.Custom,
	}
	access, err := a.eng.sign(claims)
	if err != nil {
		return TokenPair{}, err
	}

	raw, hash, err := newRawRefresh()
	if err != nil {
		return TokenPair{}, err
	}
	if err := a.store.Issue(ctx, Record{
		TokenHash: hash,
		Subject:   res.Subject,
		FamilyID:  uuid.NewString(),
		IssuedAt:  now,
		ExpiresAt: refreshExpiresAt,
		UserAgent: meta.UserAgent,
		IP:        meta.IP,
	}); err != nil {
		return TokenPair{}, xerrs.Wrap(err, xerrs.KindUnavailable, CodeStoreUnavailable, "refresh store unavailable")
	}

	return TokenPair{
		Access:           access,
		AccessExpiresAt:  accessExpiresAt,
		AccessExpiresIn:  a.accessTTL,
		RefreshRaw:       raw,
		RefreshExpiresAt: refreshExpiresAt,
		Subject:          res.Subject,
	}, nil
}

// RotateRefresh is the pure rotation primitive. Pass the raw cookie value the
// client sent; the kit hashes it, consumes the existing record (atomic, with
// OAuth-2.1 reuse detection inside the store), optionally re-reads fresh
// scopes/roles/custom via the ClaimsRefresher, and issues a new access+refresh
// pair under the same family.
//
// Errors propagated from store.Consume:
//   - *errs.Error{Code:"refresh_reused"} — the store has already revoked the
//     family before returning; the caller should clear any client-side cookie.
//   - *errs.Error{Code:"refresh_expired"} — record found but past ExpiresAt.
//   - *errs.Error{Code:"refresh_invalid"} — no matching record.
//
// rawCookie may be empty, in which case the function returns
// *errs.Error{Code:"missing_refresh"} immediately.
func (a *Auth[C]) RotateRefresh(ctx context.Context, rawCookie string, meta IssueMeta) (TokenPair, error) {
	if a.store == nil {
		return TokenPair{}, xerrs.Internal("store_unset", "auth: WithRefreshStore option was not provided")
	}
	if rawCookie == "" {
		return TokenPair{}, xerrs.Unauthorized(CodeMissingRefresh, "missing refresh cookie")
	}

	oldHash := hashRefresh(rawCookie)
	now := a.now()
	rec, err := a.store.Consume(ctx, oldHash, now)
	if err != nil {
		return TokenPair{}, err
	}

	result := LoginResult[C]{Subject: rec.Subject}
	if a.refresher != nil {
		fresh, err := a.refresher(ctx, rec.Subject)
		if err != nil {
			return TokenPair{}, err
		}
		result = fresh
		if result.Subject == "" {
			result.Subject = rec.Subject
		}
	}

	accessExpiresAt := now.Add(a.accessTTL)
	refreshExpiresAt := now.Add(a.refreshTTL)

	claims := Claims[C]{
		Subject:   result.Subject,
		IssuedAt:  now.Unix(),
		ExpiresAt: accessExpiresAt.Unix(),
		JTI:       uuid.NewString(),
		Scopes:    result.Scopes,
		Roles:     result.Roles,
		Custom:    result.Custom,
	}
	access, err := a.eng.sign(claims)
	if err != nil {
		return TokenPair{}, err
	}

	newRaw, newHash, err := newRawRefresh()
	if err != nil {
		return TokenPair{}, err
	}
	if err := a.store.Issue(ctx, Record{
		TokenHash:  newHash,
		Subject:    rec.Subject,
		FamilyID:   rec.FamilyID,
		ParentHash: oldHash,
		IssuedAt:   now,
		ExpiresAt:  refreshExpiresAt,
		UserAgent:  meta.UserAgent,
		IP:         meta.IP,
	}); err != nil {
		return TokenPair{}, xerrs.Wrap(err, xerrs.KindUnavailable, CodeStoreUnavailable, "refresh store unavailable")
	}

	return TokenPair{
		Access:           access,
		AccessExpiresAt:  accessExpiresAt,
		AccessExpiresIn:  a.accessTTL,
		RefreshRaw:       newRaw,
		RefreshExpiresAt: refreshExpiresAt,
		Subject:          result.Subject,
	}, nil
}
