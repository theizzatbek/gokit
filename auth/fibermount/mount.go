// Package fibermount wires auth.Auth[C]'s middleware factories into a
// *fibermap.Engine[T]. The bridge lives in its own subpackage so the core
// auth package does not import fibermap (preserving the outward-only
// dependency direction the kit follows).
package fibermount

import (
	"github.com/gofiber/fiber/v2"

	"github.com/theizzatbek/fibermap"
	"github.com/theizzatbek/fibermap/auth"
)

// MountMiddlewareFactories registers bearer / require_scope / require_role
// against eng under those three fixed names. For custom names, register the
// individual *Factory methods manually using fibermap.RegisterMiddlewareFactory.
//
// T is the engine's per-request data type; C is auth's custom-claims type.
// They are independent.
func MountMiddlewareFactories[T, C any](eng *fibermap.Engine[T], a *auth.Auth[C]) error {
	fibermap.RegisterMiddlewareFactory(eng, "bearer", adapt[T](a.BearerFactory))
	fibermap.RegisterMiddlewareFactory(eng, "require_scope", adapt[T](a.RequireScopeFactory))
	fibermap.RegisterMiddlewareFactory(eng, "require_role", adapt[T](a.RequireRoleFactory))
	return nil
}

// adapt bridges auth's factory signature (func([]any) (fiber.Handler, error))
// to fibermap's (func([]string) (MiddlewareFunc[T], error)). YAML factory args
// always arrive as []string; we promote them to []any for the auth factory,
// then re-wrap the produced fiber.Handler so fibermap's per-request
// Context[T] is unwrapped on entry.
func adapt[T any](authFactory func([]any) (fiber.Handler, error)) fibermap.MiddlewareFactoryFunc[T] {
	return func(args []string) (fibermap.MiddlewareFunc[T], error) {
		anyArgs := make([]any, len(args))
		for i, s := range args {
			anyArgs[i] = s
		}
		h, err := authFactory(anyArgs)
		if err != nil {
			return nil, err
		}
		return func(c *fibermap.Context[T]) error { return h(c.Ctx) }, nil
	}
}
