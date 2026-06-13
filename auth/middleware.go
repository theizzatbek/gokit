package auth

import (
	"errors"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/theizzatbek/gokit/auth/internal/principalkey"
	xerrs "github.com/theizzatbek/gokit/errs"
)

// BearerMode controls whether a missing token is fatal.
type BearerMode int

const (
	BearerRequired BearerMode = iota
	BearerOptional
)

const bearerRealm = "api"

// Bearer returns a Fiber middleware that verifies the Authorization: Bearer
// header. On success it stores a *Principal[C] in Locals under principalkey.Key{}.
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
			a.metrics.incBearerVerify("invalid")
			a.maybeSecurityLog(c, "bearer_verify_failed", err)
			return bearerReject(c, err)
		}
		if a.revokedAccess != nil && claims.JTI != "" {
			revoked, rerr := a.revokedAccess.IsRevoked(c.UserContext(), claims.JTI)
			if rerr != nil {
				// Fail-OPEN: a transient blacklist outage does not lock
				// out every user. Log and proceed; the security logger
				// gets the failure for SIEM follow-up.
				a.maybeSecurityLog(c, "revoked_access_lookup_failed", rerr)
			} else if revoked {
				a.metrics.incBearerVerify("invalid")
				a.maybeSecurityLog(c, "token_revoked", nil)
				return bearerReject(c, xerrs.Unauthorized(CodeTokenRevoked, "access token revoked"))
			}
		}
		a.metrics.incBearerVerify("ok")
		c.Locals(principalkey.Key{}, claimsToPrincipal(claims, tok))
		return c.Next()
	}
}

// bearerReject sets the RFC 6750 WWW-Authenticate challenge and returns the
// error unchanged so the application's ErrorHandler can render the body.
func bearerReject(c *fiber.Ctx, err error) error {
	code := CodeInvalidToken
	var x *xerrs.Error
	if errors.As(err, &x) {
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
// security logger via WithSecurityLogger. Silent otherwise. Use this for
// anomalies (bearer_verify_failed, refresh_reused, …).
func (a *Auth[C]) maybeSecurityLog(c *fiber.Ctx, event string, err error) {
	if a.securityLogger == nil {
		return
	}
	a.securityLogger.WarnContext(c.UserContext(), event,
		"err", err,
		"ip", a.clientIP(c),
		"ua", c.Get(fiber.HeaderUserAgent),
		"path", c.Path(),
	)
}

// maybeSecurityInfo emits a structured INFO event with ip/ua/path plus
// the caller-supplied extra attributes. Use this for non-anomaly
// SecOps-relevant events that downstream SIEMs want (login_success,
// logout, …). Silent when WithSecurityLogger was not wired.
func (a *Auth[C]) maybeSecurityInfo(c *fiber.Ctx, event string, attrs ...any) {
	if a.securityLogger == nil {
		return
	}
	args := []any{
		"ip", a.clientIP(c),
		"ua", c.Get(fiber.HeaderUserAgent),
		"path", c.Path(),
	}
	args = append(args, attrs...)
	a.securityLogger.InfoContext(c.UserContext(), event, args...)
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

// RequireAnyScope is the OR-semantic counterpart of RequireScope: the
// request passes when the principal carries AT LEAST ONE of the named
// scopes. Same 403 on miss; the WithDetails field lists every
// candidate. Use when an endpoint accepts multiple authorisation
// dimensions (e.g. `orders:read` ИЛИ `admin:all`).
func (a *Auth[C]) RequireAnyScope(scopes ...string) fiber.Handler {
	required := append([]string(nil), scopes...)
	return func(c *fiber.Ctx) error {
		p, err := MustFrom[C](c)
		if err != nil {
			return err
		}
		for _, want := range required {
			if containsString(p.Scopes, want) {
				return c.Next()
			}
		}
		details := make([]xerrs.FieldError, 0, len(required))
		for _, want := range required {
			details = append(details, xerrs.FieldError{Field: "scope", Rule: "any", Param: want, Message: "one of the listed scopes required"})
		}
		return xerrs.Permission(CodeMissingScope, "missing required scope (any-of)").WithDetails(details...)
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

// RequireAnyRole is the OR-semantic counterpart of RequireRole: the
// request passes when the principal carries AT LEAST ONE of the named
// roles.
func (a *Auth[C]) RequireAnyRole(roles ...string) fiber.Handler {
	required := append([]string(nil), roles...)
	return func(c *fiber.Ctx) error {
		p, err := MustFrom[C](c)
		if err != nil {
			return err
		}
		for _, want := range required {
			if containsString(p.Roles, want) {
				return c.Next()
			}
		}
		details := make([]xerrs.FieldError, 0, len(required))
		for _, want := range required {
			details = append(details, xerrs.FieldError{Field: "role", Rule: "any", Param: want, Message: "one of the listed roles required"})
		}
		return xerrs.Permission(CodeMissingRole, "missing required role (any-of)").WithDetails(details...)
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

// BearerFactory adapts Bearer to fibermap's middleware-factory signature.
// Accepts zero or one argument:
//
//	[]any{}             → BearerRequired (default)
//	[]any{"required"}   → BearerRequired
//	[]any{"optional"}   → BearerOptional
//
// Any other value → *errs.Error{Code: CodeInvalidFactoryArgs}, surfaced at Mount.
func (a *Auth[C]) BearerFactory(args []any) (fiber.Handler, error) {
	mode := BearerRequired
	switch len(args) {
	case 0:
		// keep required
	case 1:
		s, ok := args[0].(string)
		if !ok {
			return nil, xerrs.Internalf(CodeInvalidFactoryArgs, "bearer: expected string arg, got %T", args[0])
		}
		switch s {
		case "required":
			mode = BearerRequired
		case "optional":
			mode = BearerOptional
		default:
			return nil, xerrs.Internalf(CodeInvalidFactoryArgs, "bearer: unknown mode %q", s)
		}
	default:
		return nil, xerrs.Internalf(CodeInvalidFactoryArgs, "bearer: expected 0 or 1 args, got %d", len(args))
	}
	return a.Bearer(mode), nil
}

// RequireScopeFactory adapts RequireScope to fibermap's factory signature.
// Args are scope strings (>=1). AND semantics.
func (a *Auth[C]) RequireScopeFactory(args []any) (fiber.Handler, error) {
	scopes, err := stringSliceArgs("require_scope", args)
	if err != nil {
		return nil, err
	}
	return a.RequireScope(scopes...), nil
}

// RequireRoleFactory adapts RequireRole. Args are role strings (>=1).
func (a *Auth[C]) RequireRoleFactory(args []any) (fiber.Handler, error) {
	roles, err := stringSliceArgs("require_role", args)
	if err != nil {
		return nil, err
	}
	return a.RequireRole(roles...), nil
}

// RequireAnyScopeFactory adapts RequireAnyScope to the fibermap
// middleware-factory contract. YAML form mirrors require_scope but
// passes when the principal carries any one of the listed scopes:
//
//	middleware:
//	  - require_any_scope: ["orders:read", "admin:all"]
func (a *Auth[C]) RequireAnyScopeFactory(args []any) (fiber.Handler, error) {
	scopes, err := stringSliceArgs("require_any_scope", args)
	if err != nil {
		return nil, err
	}
	return a.RequireAnyScope(scopes...), nil
}

// RequireAnyRoleFactory adapts RequireAnyRole.
func (a *Auth[C]) RequireAnyRoleFactory(args []any) (fiber.Handler, error) {
	roles, err := stringSliceArgs("require_any_role", args)
	if err != nil {
		return nil, err
	}
	return a.RequireAnyRole(roles...), nil
}

func stringSliceArgs(name string, args []any) ([]string, error) {
	if len(args) == 0 {
		return nil, xerrs.Internalf(CodeInvalidFactoryArgs, "%s: at least one arg required", name)
	}
	out := make([]string, 0, len(args))
	for i, a := range args {
		s, ok := a.(string)
		if !ok {
			return nil, xerrs.Internalf(CodeInvalidFactoryArgs, "%s: arg %d is %T, want string", name, i, a)
		}
		out = append(out, s)
	}
	return out, nil
}
