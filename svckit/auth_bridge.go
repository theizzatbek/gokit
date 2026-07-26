package svckit

import (
	"github.com/gofiber/fiber/v2"

	"github.com/theizzatbek/gokit/auth"
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
