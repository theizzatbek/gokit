package auth

import (
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"

	xerrs "github.com/theizzatbek/fibermap/errs"
)

// BearerMode controls whether a missing token is fatal.
type BearerMode int

const (
	BearerRequired BearerMode = iota
	BearerOptional
)

const bearerRealm = "api"

// Bearer returns a Fiber middleware that verifies the Authorization: Bearer
// header. On success it stores a *Principal[C] in Locals under principalKey{}.
//
// Required mode: missing token -> 401. Optional mode: missing token -> pass through.
// In BOTH modes a present-but-invalid token is rejected with 401 - silently
// downgrading a forged token to anonymous would be a security hole.
func (a *Auth[C]) Bearer(mode BearerMode) fiber.Handler {
	return func(c *fiber.Ctx) error {
		hdr := c.Get(fiber.HeaderAuthorization)
		if hdr == "" {
			if mode == BearerOptional {
				return c.Next()
			}
			return bearerReject(c, xerrs.Unauthorized(CodeMissingToken, "missing Authorization header"))
		}
		const prefix = "Bearer "
		if !strings.HasPrefix(hdr, prefix) {
			return bearerReject(c, xerrs.Unauthorized(CodeInvalidTokenScheme, "Authorization scheme must be Bearer"))
		}
		tok := strings.TrimSpace(hdr[len(prefix):])
		if tok == "" {
			return bearerReject(c, xerrs.Unauthorized(CodeMissingToken, "Bearer token is empty"))
		}
		claims, err := a.eng.verify(tok)
		if err != nil {
			a.maybeSecurityLog(c, "bearer_verify_failed", err)
			return bearerReject(c, err)
		}
		c.Locals(principalKey{}, claimsToPrincipal(claims, tok))
		return c.Next()
	}
}

// bearerReject sets the RFC 6750 WWW-Authenticate challenge and returns the
// error unchanged so the application's ErrorHandler can render the body.
func bearerReject(c *fiber.Ctx, err error) error {
	code := CodeInvalidToken
	if x, ok := err.(*xerrs.Error); ok {
		code = x.Code
	}
	c.Set(fiber.HeaderWWWAuthenticate, wwwAuthenticate(bearerRealm, code))
	return err
}

// claimsToPrincipal projects a verified Claims[C] into the Locals-stored
// *Principal[C] consumed by From / MustFrom / scope-check helpers.
func claimsToPrincipal[C any](c Claims[C], raw string) *Principal[C] {
	return &Principal[C]{
		Subject:  c.Subject,
		Issuer:   c.Issuer,
		Audience: c.Audience,
		IssuedAt: time.Unix(c.IssuedAt, 0),
		Expires:  time.Unix(c.ExpiresAt, 0),
		JTI:      c.JTI,
		Scopes:   c.Scopes,
		Roles:    c.Roles,
		Claims:   c.Custom,
		Raw:      raw,
	}
}

// maybeSecurityLog emits a structured WARN event if the operator wired a
// security logger via WithSecurityLogger. Silent otherwise.
func (a *Auth[C]) maybeSecurityLog(c *fiber.Ctx, event string, err error) {
	if a.securityLogger == nil {
		return
	}
	a.securityLogger.WarnContext(c.UserContext(), event,
		"err", err,
		"ip", c.IP(),
		"ua", c.Get(fiber.HeaderUserAgent),
		"path", c.Path(),
	)
}

// RequireScope returns a Fiber middleware that lets the request through only if
// the principal carries every named scope (AND semantics). Missing principal
// → 500 (programmer error — bearer middleware was not installed upstream).
func (a *Auth[C]) RequireScope(scopes ...string) fiber.Handler {
	required := append([]string(nil), scopes...)
	return func(c *fiber.Ctx) error {
		p, err := MustFrom[C](c)
		if err != nil {
			return err
		}
		for _, want := range required {
			if !containsString(p.Scopes, want) {
				return xerrs.Permission(CodeMissingScope, "missing required scope").
					WithDetails(xerrs.FieldError{Field: "scope", Rule: "required", Param: want, Message: "required scope not present"})
			}
		}
		return c.Next()
	}
}

// RequireRole is RequireScope but reading from principal.Roles. Same AND
// semantics, same 403 on miss.
func (a *Auth[C]) RequireRole(roles ...string) fiber.Handler {
	required := append([]string(nil), roles...)
	return func(c *fiber.Ctx) error {
		p, err := MustFrom[C](c)
		if err != nil {
			return err
		}
		for _, want := range required {
			if !containsString(p.Roles, want) {
				return xerrs.Permission(CodeMissingRole, "missing required role").
					WithDetails(xerrs.FieldError{Field: "role", Rule: "required", Param: want, Message: "required role not present"})
			}
		}
		return c.Next()
	}
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
