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
	}
	return nil
}
