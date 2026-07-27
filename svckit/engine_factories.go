package svckit

import (
	"github.com/theizzatbek/gokit/fibermap"
)

// registerModFactories adapts the non-generic mod factories to the
// engine's type: a mod hands back func(*fiber.Ctx) error, and the
// core wraps it into fibermap.MiddlewareFunc[T], plugging in c.Ctx.
// This is the only place where T meets mod code, and exactly why Host
// and FactoryFunc got away without type parameters.
//
// Idempotent by design: New calls this once before the Wire phase and
// once again after it (see build.go), because Host.RegisterMiddlewareFactory
// is phase-agnostic — a mod calling it from Setup, Build, or Wire must
// all reach the engine. Idempotency is what makes calling it twice
// safe: each name is deleted from s.opts.modFactories as soon as it's
// registered onto the engine, so the second pass only sees names a
// mod added since the first pass ran (typically from Wire). Without
// the delete, the second call would re-register the same name and
// Engine.RegisterMiddlewareFactory would panic on the duplicate.
//
// A name that collides with an already-registered factory panics
// inside Engine.RegisterMiddlewareFactory — same as in v1, duplicate
// registration is a programmer error at startup.
func (s *Service[T, C]) registerModFactories() error {
	for name, f := range s.opts.modFactories {
		factory := f // per-iteration copy
		s.Engine.RegisterMiddlewareFactory(name,
			func(args []string) (fibermap.MiddlewareFunc[T], error) {
				h, err := factory(args)
				if err != nil {
					return nil, err
				}
				return func(c *fibermap.Context[T]) error {
					return h(c.Ctx)
				}, nil
			})
		// Idempotency: a second call to registerModFactories (after
		// Wire) must not see this name again.
		delete(s.opts.modFactories, name)
	}
	return nil
}
