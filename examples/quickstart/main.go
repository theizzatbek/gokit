// Command quickstart is the smallest fibermap demo: a single file
// boots a routed server. fiber.New, LoadFile, Mount, app.Listen, and
// graceful shutdown all live inside `eng.Run(...)`.
//
//	go run ./examples/quickstart
//
//	curl                 'http://localhost:3000/v1/patients'
//	curl -X POST         'http://localhost:3000/v1/patients?role=director'
//	curl -X POST         'http://localhost:3000/v1/patients?role=guest'      # 403
//	curl -X PUT          'http://localhost:3000/v1/patients/7?role=director'
package main

import (
	"fmt"
	"log"

	"github.com/gofiber/fiber/v2"
	"github.com/theizzatbek/gokit/fibermap"
	"github.com/theizzatbek/gokit/fibermap/factory"
)

// AppCtx is the per-request payload built by ContextBuilder.
type AppCtx struct {
	UserID string
	Role   string
}

// Ctx aliases fibermap.Context[AppCtx] so handler/middleware signatures
// read as `func(c *Ctx) error` instead of carrying the generic parameter.
type Ctx = fibermap.Context[AppCtx]

func main() {
	// Stub auth: read ?role=... from the query so you can switch roles
	// in curl. Real auth runs at the Fiber level too — see
	// examples/tasks for a Bearer-token version.
	stubAuth := func(c *fiber.Ctx) error {
		c.Locals("user_id", "u-42")
		c.Locals("role", c.Query("role", "guest"))
		return c.Next()
	}

	eng := fibermap.New[AppCtx]()

	eng.SetContextBuilder(func(c *fiber.Ctx) (AppCtx, error) {
		return AppCtx{
			UserID: c.Locals("user_id").(string),
			Role:   c.Locals("role").(string),
		}, nil
	})

	eng.RegisterMiddleware("logger", func(c *Ctx) error {
		log.Printf("→ %s %s  user=%s role=%s", c.Method(), c.Path(), c.Data.UserID, c.Data.Role)
		return c.Next()
	})
	eng.RegisterMiddleware("audit", func(c *Ctx) error {
		log.Printf("audit: %s %s by %s", c.Method(), c.Path(), c.Data.UserID)
		return c.Next()
	})

	// Built-in role guard from factory/.
	eng.RegisterMiddlewareFactory("require_role",
		factory.RequireRole(func(c *Ctx) string { return c.Data.Role }),
	)

	eng.RegisterHandler("patient.list", func(c *Ctx) error {
		return c.JSON(fiber.Map{"patients": []string{"Alice", "Bob"}, "by": c.Data.UserID})
	})
	eng.RegisterHandler("patient.create", func(c *Ctx) error {
		return c.Status(201).JSON(fiber.Map{"created": true, "by": c.Data.UserID})
	})
	eng.RegisterHandler("patient.update", func(c *Ctx) error {
		return c.JSON(fiber.Map{"updated": c.Params("id"), "by": c.Data.UserID})
	})

	// One call wraps fiber.New, LoadFile("routes.yaml"), app.Use(stubAuth),
	// engine.Mount, app.Listen(":3000"), and graceful shutdown on
	// SIGINT/SIGTERM.
	fmt.Println("Listening on :3000 — try:")
	fmt.Println("  curl                 'http://localhost:3000/v1/patients'")
	fmt.Println("  curl -X POST         'http://localhost:3000/v1/patients?role=director'")
	fmt.Println("  curl -X POST         'http://localhost:3000/v1/patients?role=guest'      # 403")
	fmt.Println("  curl -X PUT          'http://localhost:3000/v1/patients/7?role=director'")

	if err := eng.Run(fibermap.WithUse(stubAuth)); err != nil {
		log.Fatal(err)
	}
}
