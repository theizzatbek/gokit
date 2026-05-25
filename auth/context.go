package auth

import (
	"time"

	"github.com/gofiber/fiber/v2"

	xerrs "github.com/theizzatbek/gokit/errs"
)

// Principal is the authenticated identity attached to a request. It is stored
// in fiber.Locals under principalKey{} by Bearer middleware after a successful
// token verification, and read by From / MustFrom / scope-check middleware.
type Principal[C any] struct {
	Subject  string
	Issuer   string
	Audience []string
	IssuedAt time.Time
	Expires  time.Time
	JTI      string
	Scopes   []string
	Roles    []string
	Claims   C
	Raw      string
}

// principalKey is the unexported Locals key for Principal. Declared as a typed
// empty struct so two strings with the same value cannot collide.
type principalKey struct{}

// From returns the Principal stored by Bearer middleware, or (nil, false) if
// the route is anonymous (bearer:optional with no token). Does not panic.
func From[C any](c *fiber.Ctx) (*Principal[C], bool) {
	v := c.Locals(principalKey{})
	if v == nil {
		return nil, false
	}
	p, ok := v.(*Principal[C])
	if !ok {
		return nil, false
	}
	return p, true
}

// MustFrom is From for routes that ARE behind bearer:required. Missing
// Principal indicates a programmer error (forgot to wire bearer middleware
// upstream) and returns a 500-class error.
func MustFrom[C any](c *fiber.Ctx) (*Principal[C], error) {
	p, ok := From[C](c)
	if !ok {
		return nil, xerrs.Internal("missing_principal", "no auth principal in request context")
	}
	return p, nil
}

// Subject returns the principal's Subject if present, "" otherwise. Convenience
// for the common case where the route only needs the user ID.
func Subject[C any](c *fiber.Ctx) string {
	if p, ok := From[C](c); ok {
		return p.Subject
	}
	return ""
}

// HasScope returns true iff a Principal exists AND has scope s. Convenience
// for ad-hoc checks (require_scope middleware does the same for declarative
// route gating).
func HasScope[C any](c *fiber.Ctx, s string) bool {
	p, ok := From[C](c)
	if !ok {
		return false
	}
	for _, x := range p.Scopes {
		if x == s {
			return true
		}
	}
	return false
}
