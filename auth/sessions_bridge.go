package auth

import (
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/theizzatbek/gokit/auth/sessions"
)

// Sessions returns a session manager bound to *Auth[C]. The manager
// rebuilds *Principal[C] from stored Session rows and stuffs it into
// the same Locals slot Bearer uses, so RequireScope / RequireRole /
// Subject[C] / From[C] all work transparently for session-auth'd
// requests.
//
// cfg.Store is required (typically auth/sessionsredis.NewStore).
// Pass cfg.InsecureCookie=true ONLY for local dev over HTTP.
//
// Trailing options forward to sessions.NewManager — wire
// [sessions.WithMetrics] / [sessions.WithLogger] / OnIssue / OnLogout
// / OnLogoutEverywhere / OnExpire hooks here. The zero-option call
// site `a.Sessions(cfg)` stays back-compat.
//
// Errors come from sessions.NewManager: *errs.Error{Code:
// CodeInvalidConfig} for missing Store / TTL.
func (a *Auth[C]) Sessions(cfg sessions.Config, opts ...sessions.ManagerOption) (*sessions.Manager[C], error) {
	return sessions.NewManager[C](cfg,
		func(c *fiber.Ctx, subject string, scopes, roles []string, claims C, expires time.Time) {
			p := &Principal[C]{
				Subject: subject,
				Scopes:  scopes,
				Roles:   roles,
				Claims:  claims,
				Expires: expires,
			}
			c.Locals(principalKey{}, p)
		}, opts...)
}
