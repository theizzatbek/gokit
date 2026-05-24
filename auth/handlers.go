package auth

import (
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
