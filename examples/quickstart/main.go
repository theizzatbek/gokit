package main

import (
	"fmt"
	"log"

	"github.com/gofiber/fiber/v2"
	"github.com/theizzatbek/fibermap"
)

// AppCtx is the per-request payload built by ContextBuilder.
type AppCtx struct {
	UserID string
	Role   string
}

// Ctx aliases fibermap.Context[AppCtx] so handler/middleware signatures
// read as `func(c *Ctx) error` instead of carrying the generic parameter.
type Ctx = fibermap.Context[AppCtx]

// MW aliases fibermap.MiddlewareFunc[AppCtx] for the factory return type.
type MW = fibermap.MiddlewareFunc[AppCtx]

func main() {
	app := fiber.New()

	// Real auth happens at the Fiber level, BEFORE fibermap's context builder.
	// This stub reads ?role=... from the query so you can switch roles in curl.
	app.Use(func(c *fiber.Ctx) error {
		c.Locals("user_id", "u-42")
		c.Locals("role", c.Query("role", "guest"))
		return c.Next()
	})

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

	eng.RegisterMiddlewareFactory("require_role",
		func(args []string) (MW, error) {
			if len(args) == 0 {
				return nil, fmt.Errorf("require_role: at least one role required")
			}
			allowed := append([]string(nil), args...)
			return func(c *Ctx) error {
				for _, r := range allowed {
					if r == c.Data.Role {
						return c.Next()
					}
				}
				return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "forbidden"})
			}, nil
		})

	eng.RegisterHandler("patient.list", func(c *Ctx) error {
		return c.JSON(fiber.Map{"patients": []string{"Alice", "Bob"}, "by": c.Data.UserID})
	})
	eng.RegisterHandler("patient.create", func(c *Ctx) error {
		return c.Status(201).JSON(fiber.Map{"created": true, "by": c.Data.UserID})
	})
	eng.RegisterHandler("patient.update", func(c *Ctx) error {
		return c.JSON(fiber.Map{"updated": c.Params("id"), "by": c.Data.UserID})
	})

	if err := eng.LoadFile("routes.yaml"); err != nil {
		log.Fatal(err)
	}
	if err := eng.Mount(app); err != nil {
		log.Fatal(err)
	}

	fmt.Println("Registered routes:")
	for _, r := range eng.Routes() {
		fmt.Printf("  %-6s %-25s -> %-20s middleware=%v\n",
			r.Method, r.Path, r.Handler, r.Middleware)
	}
	fmt.Println()
	fmt.Println("Try:")
	fmt.Println("  curl                 'http://localhost:3000/v1/patients'")
	fmt.Println("  curl -X POST         'http://localhost:3000/v1/patients?role=director'")
	fmt.Println("  curl -X POST         'http://localhost:3000/v1/patients?role=guest'      # 403")
	fmt.Println("  curl -X PUT          'http://localhost:3000/v1/patients/7?role=director'")
	fmt.Println()

	log.Fatal(app.Listen(":3000"))
}
