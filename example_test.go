package fibermap_test

import (
	"fmt"

	"github.com/gofiber/fiber/v2"
	"github.com/theizzatbek/fibermap"
)

// docCtx is the per-request payload used by the examples below.
type docCtx struct {
	UserID string
	Role   string
}

// Alias example shows the recommended way to hide the generic parameter
// from handler signatures in user code.
type (
	docCtxRef = fibermap.Context[docCtx]
	docMW     = fibermap.MiddlewareFunc[docCtx]
)

// Example shows the full lifecycle: configure the engine, register a
// handler and a middleware, load YAML from memory, mount onto a Fiber
// router, and introspect the registered routes.
func Example() {
	eng := fibermap.New[docCtx]()

	eng.SetContextBuilder(func(c *fiber.Ctx) (docCtx, error) {
		return docCtx{UserID: "u-1", Role: "guest"}, nil
	})

	eng.RegisterMiddleware("auth", func(c *docCtxRef) error {
		return c.Next()
	})
	eng.RegisterHandler("ping", func(c *docCtxRef) error {
		return c.SendString("pong " + c.Data.UserID)
	})

	if err := eng.LoadBytes([]byte(`
groups:
  - prefix: /v1
    middleware: [auth]
    routes:
      - { method: GET, path: /ping, handler: ping }
`)); err != nil {
		panic(err)
	}
	if err := eng.Mount(fiber.New()); err != nil {
		panic(err)
	}

	fmt.Println(len(eng.Routes()), "route(s)")
	// Output: 1 route(s)
}

// ExampleEngine_RegisterMiddlewareFactory shows the parameterized
// middleware mechanism — register a factory that returns a closure
// over its YAML-supplied args, then reference it from YAML as a
// single-key map.
func ExampleEngine_RegisterMiddlewareFactory() {
	eng := fibermap.New[docCtx]()
	eng.SetContextBuilder(func(c *fiber.Ctx) (docCtx, error) {
		return docCtx{Role: "admin"}, nil
	})

	eng.RegisterMiddlewareFactory("require_role", func(args []string) (docMW, error) {
		if len(args) == 0 {
			return nil, fmt.Errorf("require_role: at least one role required")
		}
		allowed := append([]string(nil), args...)
		return func(c *docCtxRef) error {
			for _, r := range allowed {
				if r == c.Data.Role {
					return c.Next()
				}
			}
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "forbidden"})
		}, nil
	})
	eng.RegisterHandler("create", func(c *docCtxRef) error { return c.SendString("ok") })

	if err := eng.LoadBytes([]byte(`
groups:
  - routes:
      - method: POST
        path: /things
        handler: create
        middleware:
          - require_role: [admin, director]
`)); err != nil {
		panic(err)
	}

	if err := eng.Validate(); err != nil {
		fmt.Println("invalid:", err)
		return
	}
	fmt.Println("config OK")
	// Output: config OK
}

// ExampleEngine_Routes demonstrates introspection — after Mount, every
// installed route is available via Routes() with its resolved
// middleware chain (including factory args).
func ExampleEngine_Routes() {
	eng := fibermap.New[docCtx]()
	eng.SetContextBuilder(func(c *fiber.Ctx) (docCtx, error) { return docCtx{}, nil })
	eng.RegisterMiddleware("auth", func(c *docCtxRef) error { return c.Next() })
	eng.RegisterHandler("ping", func(c *docCtxRef) error { return c.SendString("pong") })

	if err := eng.LoadBytes([]byte(`
groups:
  - prefix: /v1
    middleware: [auth]
    routes:
      - { method: GET, path: /ping, handler: ping }
`)); err != nil {
		panic(err)
	}
	if err := eng.Mount(fiber.New()); err != nil {
		panic(err)
	}

	for _, r := range eng.Routes() {
		fmt.Printf("%s %s -> %s (mw=%d)\n", r.Method, r.Path, r.Handler, len(r.Middleware))
	}
	// Output: GET /v1/ping -> ping (mw=1)
}
