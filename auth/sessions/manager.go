package sessions

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/gofiber/fiber/v2"

	xerrs "github.com/theizzatbek/gokit/errs"
)

// Manager binds a SessionStore to a Config and exposes the
// per-request lifecycle: Issue, Logout, Middleware. The C type
// parameter mirrors auth.Auth[C] so issued sessions carry the
// service's claims type.
//
// Construct via [auth.Auth.Sessions] (the Auth-bound method propagates
// the same Principal[C] semantics middleware downstream rely on).
type Manager[C any] struct {
	cfg Config
	// setPrincipal stuffs the reconstructed *Principal[C] into the
	// fiber.Locals slot Bearer middleware uses, so RequireScope /
	// RequireRole / Subject[C] / From[C] all work transparently.
	// Injected by auth/sessions.NewManager so this package does
	// not import auth (preserves auth → sessions one-way edge).
	setPrincipal func(c *fiber.Ctx, subject string, scopes, roles []string, claims C, expires time.Time)
}

// NewManager returns a manager bound to cfg and a principal-setter.
// Most callers use auth.Auth.Sessions instead of touching this
// directly — the kit's Auth knows the right setter without you
// having to wire it.
func NewManager[C any](cfg Config, setPrincipal func(c *fiber.Ctx, subject string, scopes, roles []string, claims C, expires time.Time)) (*Manager[C], error) {
	if setPrincipal == nil {
		return nil, xerrs.Validation(CodeInvalidConfig, "sessions: setPrincipal is required")
	}
	cfg = cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &Manager[C]{cfg: cfg, setPrincipal: setPrincipal}, nil
}

// Issue creates a new session for subject, writes the cookie, and
// returns nil on success. claims is the service-defined C — it is
// JSON-encoded and persisted so it survives across cookie reads.
//
// scopes / roles propagate to the Principal[C] the Middleware
// rebuilds — pass empty when not used.
func (m *Manager[C]) Issue(c *fiber.Ctx, subject string, claims C, scopes, roles []string) error {
	id, err := newID()
	if err != nil {
		return xerrs.Wrap(err, xerrs.KindInternal, CodeStoreFailed,
			"sessions: id generation failed")
	}
	raw, err := json.Marshal(claims)
	if err != nil {
		return xerrs.Wrap(err, xerrs.KindValidation, CodeClaimsDecode,
			"sessions: claims encode failed")
	}
	now := time.Now()
	sess := &Session{
		ID:         id,
		Subject:    subject,
		Scopes:     scopes,
		Roles:      roles,
		Claims:     raw,
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  now.Add(m.cfg.TTL),
	}
	if err := m.cfg.Store.Create(c.UserContext(), sess); err != nil {
		return xerrs.Wrap(err, xerrs.KindUnavailable, CodeStoreFailed,
			"sessions: store create failed")
	}
	m.setCookie(c, id, sess.ExpiresAt)
	return nil
}

// Logout deletes the session referenced by the cookie and clears
// the cookie. Idempotent — missing cookie / unknown ID is not an
// error.
func (m *Manager[C]) Logout(c *fiber.Ctx) error {
	id := c.Cookies(m.cfg.CookieName)
	if id != "" {
		if err := m.cfg.Store.Delete(c.UserContext(), id); err != nil {
			return xerrs.Wrap(err, xerrs.KindUnavailable, CodeStoreFailed,
				"sessions: store delete failed")
		}
	}
	m.clearCookie(c)
	return nil
}

// LogoutEverywhere removes every active session for the supplied
// subject (admin "force logout"). Returns Unsupported when the
// store can't enumerate by subject.
func (m *Manager[C]) LogoutEverywhere(ctx context.Context, subject string) error {
	if err := m.cfg.Store.DeleteForSubject(ctx, subject); err != nil {
		return xerrs.Wrap(err, xerrs.KindUnavailable, CodeStoreFailed,
			"sessions: bulk delete failed")
	}
	return nil
}

// Middleware returns the Fiber middleware that reads the cookie,
// loads the session, populates *Principal[C] in Locals, and (in
// Required mode) rejects the request when no valid session exists.
//
// Side-effects:
//   - Sliding refresh: a successful read advances LastSeenAt to NOW
//     and bumps ExpiresAt to NOW + IdleTimeout (capped at
//     CreatedAt + TTL).
//   - Expired sessions are deleted in-line so the next hit doesn't
//     re-load them.
func (m *Manager[C]) Middleware(mode Mode) fiber.Handler {
	return func(c *fiber.Ctx) error {
		id := c.Cookies(m.cfg.CookieName)
		if id == "" {
			return m.failOrNext(c, mode, "no cookie")
		}
		if !validID(id) {
			m.clearCookie(c)
			return m.failOrNext(c, mode, "invalid cookie shape")
		}
		ctx := c.UserContext()
		sess, err := m.cfg.Store.Get(ctx, id)
		if err != nil {
			return xerrs.Wrap(err, xerrs.KindUnavailable, CodeStoreFailed,
				"sessions: store get failed")
		}
		if sess == nil {
			m.clearCookie(c)
			return m.failOrNext(c, mode, "no such session")
		}
		now := time.Now()
		if !sess.ExpiresAt.IsZero() && now.After(sess.ExpiresAt) {
			_ = m.cfg.Store.Delete(ctx, id)
			m.clearCookie(c)
			return m.failOrNext(c, mode, "session expired")
		}

		var claims C
		if len(sess.Claims) > 0 {
			if err := json.Unmarshal(sess.Claims, &claims); err != nil {
				// Schema drift between deploys. Force a logout
				// rather than 500 — easier to recover by
				// re-logging-in.
				_ = m.cfg.Store.Delete(ctx, id)
				m.clearCookie(c)
				return m.failOrNext(c, mode, "claims decode")
			}
		}

		// Sliding refresh, capped at CreatedAt + TTL.
		newExp := now.Add(m.cfg.IdleTimeout)
		absLimit := sess.CreatedAt.Add(m.cfg.TTL)
		if newExp.After(absLimit) {
			newExp = absLimit
		}
		if newExp.After(sess.ExpiresAt) {
			_ = m.cfg.Store.Touch(ctx, id, now, newExp)
			m.setCookie(c, id, newExp)
		}

		m.setPrincipal(c, sess.Subject, sess.Scopes, sess.Roles, claims, newExp)
		return c.Next()
	}
}

func (m *Manager[C]) failOrNext(c *fiber.Ctx, mode Mode, reason string) error {
	if mode == Required {
		return xerrs.Unauthorizedf(CodeMissingSession, "session required (%s)", reason)
	}
	_ = reason
	return c.Next()
}

// setCookie writes the kit-managed cookie. expires controls both
// the cookie's Expires + the cookie's Max-Age attributes (Fiber
// derives Max-Age from Expires).
func (m *Manager[C]) setCookie(c *fiber.Ctx, value string, expires time.Time) {
	c.Cookie(&fiber.Cookie{
		Name:     m.cfg.CookieName,
		Value:    value,
		Path:     m.cfg.Path,
		Domain:   m.cfg.Domain,
		Expires:  expires,
		HTTPOnly: true,
		Secure:   !m.cfg.InsecureCookie,
		SameSite: m.cfg.SameSite,
	})
}

func (m *Manager[C]) clearCookie(c *fiber.Ctx) {
	c.Cookie(&fiber.Cookie{
		Name:     m.cfg.CookieName,
		Value:    "",
		Path:     m.cfg.Path,
		Domain:   m.cfg.Domain,
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HTTPOnly: true,
		Secure:   !m.cfg.InsecureCookie,
		SameSite: m.cfg.SameSite,
	})
}

// Sentinel — keep imports tidy when test-only paths drop usage.
var _ = errors.New
