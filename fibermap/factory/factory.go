// Package factory ships ready-made middleware factories and adapters
// for [fibermap.Engine].
//
// fibermap factories are parameterized middleware — registered once,
// invoked from YAML as {name: [args...]}. The helpers in this package
// cover the patterns that show up in nearly every project:
//
//   - RequireRole / RequireAnyScope — role / OAuth-scope guards. They
//     take an accessor that knows how to pull the role (or scope set)
//     from your per-request Context[T].Data, so they're agnostic to
//     your AppCtx shape.
//   - Adapter / AdapterFactory — bridge any plain fiber.Handler or
//     parameterized fiber.Handler producer into the fibermap signature.
//     Useful when you want to register fiber's stock middleware
//     (cors, requestid, limiter, etc) through fibermap.
//
// All helpers are generic over T (the per-request payload). Typical
// wiring:
//
//	import (
//	    "github.com/gofiber/fiber/v2/middleware/requestid"
//	    "github.com/theizzatbek/gokit/fibermap"
//	    "github.com/theizzatbek/gokit/fibermap/factory"
//	)
//
//	type AppCtx struct{ Role string; Scopes []string }
//
//	eng := fibermap.New[AppCtx]()
//	eng.RegisterMiddlewareFactory("require_role",
//	    factory.RequireRole[AppCtx](func(c *fibermap.Context[AppCtx]) string {
//	        return c.Data.Role
//	    }),
//	)
//	eng.RegisterMiddlewareFactory("require_scope",
//	    factory.RequireAnyScope[AppCtx](func(c *fibermap.Context[AppCtx]) []string {
//	        return c.Data.Scopes
//	    }),
//	)
//	eng.RegisterMiddleware("request_id",
//	    factory.Adapter[AppCtx](requestid.New()),
//	)
package factory

import (
	"errors"

	"github.com/gofiber/fiber/v2"
	"github.com/theizzatbek/gokit/fibermap"
)

// Option customizes the response a guard returns when access is denied.
// The default is `403 {"error":"forbidden"}`.
type Option func(*config)

type config struct {
	deny fiber.Handler
}

// WithDenyHandler overrides the default forbidden response. The handler
// is invoked instead of writing 403; whatever it writes is the response.
func WithDenyHandler(h fiber.Handler) Option {
	return func(c *config) { c.deny = h }
}

func newConfig(opts []Option) config {
	c := config{
		deny: func(c *fiber.Ctx) error {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "forbidden"})
		},
	}
	for _, opt := range opts {
		opt(&c)
	}
	return c
}

// RoleAccessor extracts the request's role from the per-request payload.
type RoleAccessor[T any] func(c *fibermap.Context[T]) string

// ScopeAccessor extracts the request's scope set from the per-request payload.
type ScopeAccessor[T any] func(c *fibermap.Context[T]) []string

// RequireRole returns a factory that allows the request when the role
// returned by `get` is one of the YAML args. Empty args list rejects
// the registration at Mount via CodeInvalidFactoryArgs.
//
//	eng.RegisterMiddlewareFactory("require_role",
//	    factory.RequireRole[AppCtx](func(c *fibermap.Context[AppCtx]) string {
//	        return c.Data.Role
//	    }),
//	)
//
// YAML:
//
//	middleware:
//	  - require_role: [director, receptionist]
func RequireRole[T any](get RoleAccessor[T], opts ...Option) fibermap.MiddlewareFactoryFunc[T] {
	cfg := newConfig(opts)
	return func(args []string) (fibermap.MiddlewareFunc[T], error) {
		if len(args) == 0 {
			return nil, errors.New("require_role: at least one role required")
		}
		allowed := append([]string(nil), args...)
		return func(c *fibermap.Context[T]) error {
			role := get(c)
			for _, r := range allowed {
				if r == role {
					return c.Next()
				}
			}
			return cfg.deny(c.Ctx)
		}, nil
	}
}

// RequireAnyScope returns a factory that allows the request when the
// scope set returned by `get` intersects with the YAML args. Empty
// args list rejects the registration at Mount via
// CodeInvalidFactoryArgs.
//
// "Any" semantics mirror OAuth/OIDC conventions: a token with scope A
// passes a guard declaring `[A, B]`. For all-of semantics, stack two
// `require_scope` calls.
func RequireAnyScope[T any](get ScopeAccessor[T], opts ...Option) fibermap.MiddlewareFactoryFunc[T] {
	cfg := newConfig(opts)
	return func(args []string) (fibermap.MiddlewareFunc[T], error) {
		if len(args) == 0 {
			return nil, errors.New("require_scope: at least one scope required")
		}
		required := append([]string(nil), args...)
		return func(c *fibermap.Context[T]) error {
			scopes := get(c)
			for _, want := range required {
				for _, have := range scopes {
					if have == want {
						return c.Next()
					}
				}
			}
			return cfg.deny(c.Ctx)
		}, nil
	}
}

// Adapter bridges a plain fiber.Handler into the fibermap MiddlewareFunc
// signature. Use it to register stock Fiber middleware (cors,
// requestid, limiter, …) with [fibermap.Engine.RegisterMiddleware].
//
//	eng.RegisterMiddleware("request_id",
//	    factory.Adapter[AppCtx](requestid.New()),
//	)
func Adapter[T any](h fiber.Handler) fibermap.MiddlewareFunc[T] {
	return func(c *fibermap.Context[T]) error {
		return h(c.Ctx)
	}
}

// AdapterFactory bridges a parameterized fiber.Handler producer into
// the fibermap MiddlewareFactoryFunc signature. Use it for stock Fiber
// middleware whose constructor takes config that's expressible as
// YAML strings:
//
//	eng.RegisterMiddlewareFactory("cors",
//	    factory.AdapterFactory[AppCtx](func(args []string) (fiber.Handler, error) {
//	        return cors.New(cors.Config{AllowOrigins: strings.Join(args, ",")}), nil
//	    }),
//	)
//
// YAML:
//
//	middleware:
//	  - cors: ["https://example.com", "https://app.example.com"]
func AdapterFactory[T any](new func(args []string) (fiber.Handler, error)) fibermap.MiddlewareFactoryFunc[T] {
	return func(args []string) (fibermap.MiddlewareFunc[T], error) {
		h, err := new(args)
		if err != nil {
			return nil, err
		}
		return Adapter[T](h), nil
	}
}
