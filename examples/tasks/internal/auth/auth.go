// Package auth holds the Fiber-level auth middleware (runs BEFORE
// fibermap's ContextBuilder so it can stash user info in Locals)
// and the fibermap-level require_role factory.
package auth

import (
	"errors"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/theizzatbek/fibermap"
	"github.com/theizzatbek/fibermap/examples/tasks/internal/appctx"
)

// Demo token table — in a real app, this is a JWT verifier hitting
// the public key, an opaque-token lookup against Redis, etc. The
// shape (token -> {user_id, role}) stays the same.
var demoTokens = map[string]struct {
	UserID string
	Role   string
}{
	"alice-token": {UserID: "u-alice", Role: "user"},
	"bob-token":   {UserID: "u-bob", Role: "user"},
	"root-token":  {UserID: "u-root", Role: "admin"},
}

// Bearer is a Fiber-level middleware (install via app.Use) that
// parses `Authorization: Bearer <token>`, looks the token up, and
// writes user_id + role into c.Locals. 401 on missing/unknown.
//
// MUST run before fibermap.Engine.Mount so the ContextBuilder can
// read the locals it just set.
func Bearer() fiber.Handler {
	return func(c *fiber.Ctx) error {
		h := c.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(h, prefix) {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "missing bearer token"})
		}
		token := strings.TrimPrefix(h, prefix)
		u, ok := demoTokens[token]
		if !ok {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "invalid token"})
		}
		c.Locals("user_id", u.UserID)
		c.Locals("role", u.Role)
		return c.Next()
	}
}

// RequireRole returns a MiddlewareFactoryFunc to register with the
// engine via:
//
//	eng.RegisterMiddlewareFactory("require_role", auth.RequireRole)
//
// YAML calls it as `{require_role: [admin]}` or `{require_role: [user, admin]}`.
func RequireRole(args []string) (appctx.MW, error) {
	if len(args) == 0 {
		return nil, errors.New("require_role: at least one role required")
	}
	allowed := append([]string(nil), args...)
	return func(c *appctx.Ctx) error {
		for _, r := range allowed {
			if r == c.Data.Role {
				return c.Next()
			}
		}
		// Use the request-scoped logger so the auth denial is correlated
		// with the rest of the request log line.
		c.Data.Log.Warn("authz denied",
			"required_roles", allowed,
			"actual_role", c.Data.Role,
			"path", c.Path())
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error":          "forbidden",
			"required_roles": allowed,
			"current_role":   c.Data.Role,
		})
	}, nil
}

// Confirm the factory satisfies fibermap's signature at compile time.
var _ fibermap.MiddlewareFactoryFunc[appctx.AppCtx] = RequireRole
