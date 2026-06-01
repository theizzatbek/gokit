// Package appctx defines the per-request AppCtx and a ContextBuilder
// that populates UserID from Bearer claims.
package appctx

import (
	"log/slog"

	"github.com/gofiber/fiber/v2"
	"github.com/theizzatbek/gokit/auth"
)

// AppCtx is the per-request context the fibermap engine carries.
type AppCtx struct {
	Log    *slog.Logger
	UserID string // empty on public routes; subject from JWT claims
}

// NewContextBuilder reads the Bearer principal placed in Locals by the
// auth middleware and stashes its subject as UserID. Public routes (no
// middleware) get an empty UserID. The logger is enriched per-request
// with user_id when set.
func NewContextBuilder[C any](authObj *auth.Auth[C], base *slog.Logger) func(*fiber.Ctx) (AppCtx, error) {
	if base == nil {
		base = slog.Default()
	}
	return func(c *fiber.Ctx) (AppCtx, error) {
		uid := authObj.Subject(c) // returns "" when no principal in Locals
		log := base
		if uid != "" {
			log = base.With("user_id", uid)
		}
		return AppCtx{Log: log, UserID: uid}, nil
	}
}
