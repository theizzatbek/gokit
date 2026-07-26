package svckit

import (
	"github.com/gofiber/fiber/v2"

	"github.com/theizzatbek/gokit/auth"
	"github.com/theizzatbek/gokit/fibermap"
)

// subjectKeyFn turns a typed Auth[C] into a non-generic closure for
// Host.SubjectKeyFn. This is the only place where C "collapses": a
// mod gets a ready-made function, not a type parameter — that's how
// Host stays free of type parameters.
func subjectKeyFn[C any](a *auth.Auth[C]) func(*fiber.Ctx) string {
	if a == nil {
		return nil
	}
	return auth.KeyBySubject[C]
}

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
