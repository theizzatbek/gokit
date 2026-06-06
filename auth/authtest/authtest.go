// Package authtest exposes test-only helpers for the auth package
// that production code MUST NOT depend on. Keeps the production
// auth/ surface lean — anything an integration test needs to fake
// an authenticated request lives here, not next to the middleware.
package authtest

import (
	"github.com/gofiber/fiber/v2"

	"github.com/theizzatbek/gokit/auth"
	"github.com/theizzatbek/gokit/auth/internal/principalkey"
)

// SetPrincipal stores p under the Locals slot Bearer / API-key /
// session-bridge middleware normally populates, so downstream code
// that calls auth.From[C](c) / auth.Subject[C](c) sees it as if a
// real token had been verified.
//
// Intended for integration tests of cross-cutting layers (Sentry
// user scope, request-scoped logging, metrics labels) that depend
// on principal presence but don't want to mint a real JWT.
//
// Lives in a sibling package so production imports of auth can
// never reach this — surfacing accidental use in greps and code
// review.
func SetPrincipal[C any](c *fiber.Ctx, p *auth.Principal[C]) {
	c.Locals(principalkey.Key{}, p)
}
