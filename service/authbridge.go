package service

import (
	"github.com/gofiber/fiber/v2"

	"github.com/theizzatbek/gokit/auth"
	"github.com/theizzatbek/gokit/fibermap"
)

// authSubjectBridge returns a tiny middleware that pulls the
// authenticated subject out of auth's private Locals slot and
// stores it under [fibermap.LocalsAuthSubject] for
// [fibermap.LoggerFrom] to pick up.
//
// auth's Principal[C] is generic over the custom-claims type, so
// fibermap can't read it directly without dragging the type
// parameter through every package. The bridge translates the
// principal into a plain `string` subject under a shared key —
// no type dependency, same effect.
//
// No-op when no principal is present (anonymous request).
func (s *Service[T, C]) authSubjectBridge() fiber.Handler {
	return func(c *fiber.Ctx) error {
		if p, ok := auth.From[C](c); ok && p != nil && p.Subject != "" {
			c.Locals(fibermap.LocalsAuthSubject, p.Subject)
		}
		return c.Next()
	}
}
