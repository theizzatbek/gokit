package auth

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	xerrs "github.com/theizzatbek/fibermap/errs"
)

// loginResponse is the wire shape of POST /auth/login on success.
type loginResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
	Subject     string `json:"subject"`
}

const refreshCookieName = "refresh_token"

// loginValidator is package-private — one instance amortises validator's
// reflection cache across requests.
var loginValidator = validator.New(validator.WithRequiredStructEnabled())

// LoginHandler verifies credentials via the registered CredentialsVerifier,
// issues a new access JWT + opaque refresh, sets the refresh cookie, and
// returns the access in JSON.
func (a *Auth[C]) LoginHandler(c *fiber.Ctx) error {
	if a.verifier == nil {
		return xerrs.Internal("verifier_unset", "auth: SetCredentialsVerifier was not called")
	}
	if a.store == nil {
		return xerrs.Internal("store_unset", "auth: WithRefreshStore option was not provided")
	}

	var req LoginRequest
	if err := c.BodyParser(&req); err != nil {
		return xerrs.Wrap(err, xerrs.KindValidation, "invalid_body", "could not decode login body")
	}
	if err := loginValidator.Struct(&req); err != nil {
		return xerrs.Wrap(err, xerrs.KindValidation, "invalid_body", "login body failed validation")
	}

	res, err := a.verifier(c.UserContext(), req)
	if err != nil {
		return err
	}

	now := a.now()
	claims := Claims[C]{
		Subject:   res.Subject,
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(a.accessTTL).Unix(),
		JTI:       uuid.NewString(),
		Scopes:    res.Scopes,
		Roles:     res.Roles,
		Custom:    res.Custom,
	}
	access, err := a.eng.sign(claims)
	if err != nil {
		return err
	}

	raw, hash, err := newRawRefresh()
	if err != nil {
		return err
	}
	familyID := uuid.NewString()
	if err := a.store.Issue(c.UserContext(), Record{
		TokenHash: hash,
		Subject:   res.Subject,
		FamilyID:  familyID,
		IssuedAt:  now,
		ExpiresAt: now.Add(a.refreshTTL),
		UserAgent: c.Get(fiber.HeaderUserAgent),
		IP:        c.IP(),
	}); err != nil {
		return xerrs.Wrap(err, xerrs.KindUnavailable, CodeStoreUnavailable, "refresh store unavailable")
	}

	// Make the principal visible to downstream middleware in the same request
	// (audit loggers etc.).
	c.Locals(principalKey{}, claimsToPrincipal(claims, access))

	a.setRefreshCookie(c, raw, now.Add(a.refreshTTL))
	return c.Status(http.StatusOK).JSON(loginResponse{
		AccessToken: access,
		TokenType:   "Bearer",
		ExpiresIn:   int64(a.accessTTL.Seconds()),
		Subject:     res.Subject,
	})
}

// RefreshHandler reads the refresh cookie, atomically rotates it through the
// store, and returns a new access token + a fresh refresh cookie.
//
// On reuse detection (already-consumed/revoked), the store has already revoked
// the whole token family by the time it returns the error — this handler just
// clears the bad cookie and 401s.
func (a *Auth[C]) RefreshHandler(c *fiber.Ctx) error {
	if a.store == nil {
		return xerrs.Internal("store_unset", "auth: WithRefreshStore option was not provided")
	}
	raw := c.Cookies(refreshCookieName)
	if raw == "" {
		return xerrs.Unauthorized(CodeMissingRefresh, "missing refresh cookie")
	}
	oldHash := hashRefresh(raw)
	now := a.now()
	rec, err := a.store.Consume(c.UserContext(), oldHash, now)
	if err != nil {
		if e, ok := errors.AsType[*xerrs.Error](err); ok && e.Code == CodeRefreshReused {
			a.maybeSecurityLog(c, "refresh_reused", err)
		}
		a.clearRefreshCookie(c)
		return err
	}

	// Re-read fresh claims if a refresher is registered; otherwise carry only
	// the subject from the rotated record.
	result := LoginResult[C]{Subject: rec.Subject}
	if a.refresher != nil {
		fresh, err := a.refresher(c.UserContext(), rec.Subject)
		if err != nil {
			return err
		}
		result = fresh
		if result.Subject == "" {
			result.Subject = rec.Subject
		}
	}

	claims := Claims[C]{
		Subject:   result.Subject,
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(a.accessTTL).Unix(),
		JTI:       uuid.NewString(),
		Scopes:    result.Scopes,
		Roles:     result.Roles,
		Custom:    result.Custom,
	}
	access, err := a.eng.sign(claims)
	if err != nil {
		return err
	}
	newRaw, newHash, err := newRawRefresh()
	if err != nil {
		return err
	}
	if err := a.store.Issue(c.UserContext(), Record{
		TokenHash:  newHash,
		Subject:    rec.Subject,
		FamilyID:   rec.FamilyID,
		ParentHash: oldHash,
		IssuedAt:   now,
		ExpiresAt:  now.Add(a.refreshTTL),
		UserAgent:  c.Get(fiber.HeaderUserAgent),
		IP:         c.IP(),
	}); err != nil {
		return xerrs.Wrap(err, xerrs.KindUnavailable, CodeStoreUnavailable, "refresh store unavailable")
	}
	a.setRefreshCookie(c, newRaw, now.Add(a.refreshTTL))
	return c.Status(http.StatusOK).JSON(loginResponse{
		AccessToken: access,
		TokenType:   "Bearer",
		ExpiresIn:   int64(a.accessTTL.Seconds()),
		Subject:     rec.Subject,
	})
}

func (a *Auth[C]) setRefreshCookie(c *fiber.Ctx, raw string, expires time.Time) {
	c.Cookie(&fiber.Cookie{
		Name:     refreshCookieName,
		Value:    raw,
		Path:     a.cookiePath,
		Domain:   a.cookieDomain,
		Expires:  expires,
		MaxAge:   int(a.refreshTTL.Seconds()),
		HTTPOnly: true,
		Secure:   a.cookieSecure,
		SameSite: "Strict",
	})
}

func (a *Auth[C]) clearRefreshCookie(c *fiber.Ctx) {
	c.Cookie(&fiber.Cookie{
		Name:     refreshCookieName,
		Value:    "",
		Path:     a.cookiePath,
		Domain:   a.cookieDomain,
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HTTPOnly: true,
		Secure:   a.cookieSecure,
		SameSite: "Strict",
	})
}

// LogoutHandler revokes the entire token family of the supplied refresh cookie
// and clears the cookie. Idempotent: a missing or already-revoked cookie still
// returns 204.
func (a *Auth[C]) LogoutHandler(c *fiber.Ctx) error {
	raw := c.Cookies(refreshCookieName)
	if raw == "" {
		return c.SendStatus(http.StatusNoContent)
	}
	if a.store == nil {
		a.clearRefreshCookie(c)
		return c.SendStatus(http.StatusNoContent)
	}
	hash := hashRefresh(raw)
	// Consume reveals FamilyID even when the record is already consumed/revoked
	// (it errors but the side-effect is a family revoke). For a clean logout we
	// prefer a direct lookup: try a no-op Consume; ignore the error.
	rec, err := a.store.Consume(c.UserContext(), hash, a.now())
	if err == nil {
		_ = a.store.RevokeFamily(c.UserContext(), rec.FamilyID)
	}
	a.clearRefreshCookie(c)
	return c.SendStatus(http.StatusNoContent)
}

// LogoutAllHandler revokes every refresh token belonging to the current
// principal's Subject. Must be mounted behind Bearer middleware so principal
// is available.
func (a *Auth[C]) LogoutAllHandler(c *fiber.Ctx) error {
	p, err := MustFrom[C](c)
	if err != nil {
		return err
	}
	if a.store != nil {
		if err := a.store.RevokeSubject(c.UserContext(), p.Subject); err != nil {
			return xerrs.Wrap(err, xerrs.KindUnavailable, CodeStoreUnavailable, "refresh store unavailable")
		}
	}
	a.clearRefreshCookie(c)
	return c.SendStatus(http.StatusNoContent)
}
