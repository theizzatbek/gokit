package auth

import (
	"errors"
	"net/http"
	"time"

	"github.com/gofiber/fiber/v2"

	xerrs "github.com/theizzatbek/gokit/errs"
)

// loginResponse is the wire shape of the default JSON body written by
// IssueLogin / IssueRefresh on success. Callers wanting a different shape
// should call IssueTokens / RotateRefresh directly and write their own JSON.
type loginResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
	Subject     string `json:"subject"`
}

const refreshCookieName = "refresh_token"

// IssueLogin is the Fiber-aware wrapper around IssueTokens. The caller has
// already verified credentials (whatever shape they take — password, PKCS7,
// mTLS, SSO — kit does not care) and constructed a LoginResult; this method
// mints the access+refresh pair, sets the HttpOnly refresh cookie, and
// writes the default JSON body.
//
//	// Inside your fibermap handler:
//	user, err := svc.Authenticate(c.UserContext(), body.Login, body.Password)
//	if err != nil {
//	    return err
//	}
//	return authObj.IssueLogin(c.Ctx, auth.LoginResult[Claims]{
//	    Subject: user.ID,
//	    Custom:  Claims{Email: user.Email},
//	})
//
// For a custom response shape (e.g. add a session id, switch from cookie to
// header, return refresh token in body), call IssueTokens directly.
func (a *Auth[C]) IssueLogin(c *fiber.Ctx, res LoginResult[C]) error {
	pair, err := a.IssueTokens(c.UserContext(), res, IssueMeta{
		UserAgent: c.Get(fiber.HeaderUserAgent),
		IP:        c.IP(),
	})
	if err != nil {
		return err
	}

	c.Locals(principalKey{}, &Principal[C]{
		Subject: pair.Subject,
		Scopes:  res.Scopes,
		Roles:   res.Roles,
		Claims:  res.Custom,
		Raw:     pair.Access,
		Expires: pair.AccessExpiresAt,
	})

	a.maybeSecurityInfo(c, "login_success", "subject", pair.Subject)
	a.setRefreshCookie(c, pair.RefreshRaw, pair.RefreshExpiresAt)
	return c.Status(http.StatusOK).JSON(loginResponse{
		AccessToken: pair.Access,
		TokenType:   "Bearer",
		ExpiresIn:   int64(pair.AccessExpiresIn.Seconds()),
		Subject:     pair.Subject,
	})
}

// IssueRefresh is the Fiber-aware wrapper around RotateRefresh. Reads the
// refresh cookie, rotates it through the store, and writes a new
// access+cookie pair to the response.
//
// On reuse detection the store has already revoked the entire token family
// by the time RotateRefresh returns the error — this wrapper just clears
// the bad cookie and propagates the 401.
//
// Mount as a Fiber handler:
//
//	app.Post("/auth/refresh", authObj.IssueRefresh)
//
// Or via fibermap:
//
//	fibermap.RegisterHandler(eng, "auth.refresh",
//	    func(c *fibermap.Context[T]) error { return authObj.IssueRefresh(c.Ctx) })
func (a *Auth[C]) IssueRefresh(c *fiber.Ctx) error {
	raw := c.Cookies(refreshCookieName)
	pair, err := a.RotateRefresh(c.UserContext(), raw, IssueMeta{
		UserAgent: c.Get(fiber.HeaderUserAgent),
		IP:        c.IP(),
	})
	if err != nil {
		if e, ok := errors.AsType[*xerrs.Error](err); ok && e.Code == CodeRefreshReused {
			a.maybeSecurityLog(c, "refresh_reused", err)
		}
		a.clearRefreshCookie(c)
		return err
	}
	a.setRefreshCookie(c, pair.RefreshRaw, pair.RefreshExpiresAt)
	return c.Status(http.StatusOK).JSON(loginResponse{
		AccessToken: pair.Access,
		TokenType:   "Bearer",
		ExpiresIn:   int64(pair.AccessExpiresIn.Seconds()),
		Subject:     pair.Subject,
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

// Logout revokes the entire token family of the supplied refresh cookie and
// clears the cookie. Idempotent: a missing or already-revoked cookie still
// returns 204.
func (a *Auth[C]) Logout(c *fiber.Ctx) error {
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
		a.maybeSecurityInfo(c, "logout", "subject", rec.Subject)
	}
	a.clearRefreshCookie(c)
	return c.SendStatus(http.StatusNoContent)
}

// LogoutAll revokes every refresh token belonging to the current principal's
// Subject. Must be mounted behind Bearer middleware so principal is available.
func (a *Auth[C]) LogoutAll(c *fiber.Ctx) error {
	p, err := MustFrom[C](c)
	if err != nil {
		return err
	}
	if a.store != nil {
		if err := a.store.RevokeSubject(c.UserContext(), p.Subject); err != nil {
			return xerrs.Wrap(err, xerrs.KindUnavailable, CodeStoreUnavailable, "refresh store unavailable")
		}
	}
	a.maybeSecurityInfo(c, "logout_all", "subject", p.Subject)
	a.clearRefreshCookie(c)
	return c.SendStatus(http.StatusNoContent)
}
