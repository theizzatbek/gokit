// Package admin exposes admin-only ops endpoints. Right now: an
// introspection handler that returns the full route table fibermap
// registered, as JSON. Useful for the on-call to ask "what's actually
// wired right now?" without grepping config.
package admin

import (
	"github.com/gofiber/fiber/v2"
	"github.com/theizzatbek/gokit/examples/tasks/internal/appctx"
	"github.com/theizzatbek/gokit/fibermap"
)

// Routes returns a handler that serves the current Engine.Routes()
// snapshot. The handler captures the engine by closure so it can call
// introspection without going through any registry. Wire it as:
//
//	eng.RegisterHandler("admin.routes", admin.Routes(eng))
//
// Output uses RouteInfo's json tags directly — no wrapper struct.
func Routes(eng *fibermap.Engine[appctx.AppCtx]) appctx.H {
	return func(c *appctx.Ctx) error {
		return c.Status(fiber.StatusOK).JSON(fiber.Map{
			"routes": eng.Routes(),
		})
	}
}
