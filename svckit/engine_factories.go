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
// Two mods claiming the SAME name across different phases (one during
// Build, another during Wire) can't reach that panic either: by the
// time this loop runs a second time, hostImpl.RegisterMiddlewareFactory
// has already refused to re-queue a name that modFactoriesDone (below)
// says was handed to the engine in the first pass — see that method
// for the collision check itself. This loop only needs to mark names
// done as they land; it never has to worry about seeing the same name
// twice across two calls.
//
// A mod's name colliding with a CORE-reserved factory name (`bearer`,
// `require_scope`, ... — registered directly on Engine by
// mountAuthMiddleware, outside this map entirely) still panics inside
// Engine.RegisterMiddlewareFactory — same as in v1, that's a
// programmer error the core has no visibility into ahead of time.
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
		// Wire) must not see this name again...
		delete(s.opts.modFactories, name)
		// ...and hostImpl.RegisterMiddlewareFactory must be able to
		// tell a later collision on this name apart from a fresh
		// registration, even though it's no longer in the map above.
		if s.opts.modFactoriesDone == nil {
			s.opts.modFactoriesDone = map[string]struct{}{}
		}
		s.opts.modFactoriesDone[name] = struct{}{}
	}
	return nil
}
