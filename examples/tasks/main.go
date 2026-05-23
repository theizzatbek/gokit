// Command tasks is a realistic fibermap example: small CRUD API for
// per-user task lists, with project layout, Bearer auth, structured
// logging, request IDs, role-based admin endpoints, graceful shutdown,
// and route introspection — everything you'd want as a starting
// template, none of the toy stuff.
//
// Try it:
//
//	go run ./examples/tasks
//
//	# default tokens: alice-token / bob-token (role=user), root-token (role=admin)
//	curl -H "Authorization: Bearer alice-token"            http://localhost:3000/api/v1/tasks
//	curl -H "Authorization: Bearer alice-token" \
//	     -H "Content-Type: application/json" \
//	     -d '{"title":"buy milk"}' \
//	     http://localhost:3000/api/v1/tasks
//	curl -X DELETE -H "Authorization: Bearer alice-token"  http://localhost:3000/api/v1/tasks/<id>   # 403
//	curl -X DELETE -H "Authorization: Bearer root-token"   http://localhost:3000/api/v1/tasks/<id>   # 204
//	curl -H "Authorization: Bearer root-token"             http://localhost:3000/api/v1/admin/routes
//
//	# The GET /tasks endpoint is cached for 10s per-user (see
//	# routes.yaml + SetCacheDefaults wiring below). The second call
//	# within 10s does not invoke the handler.
//	curl -H "Authorization: Bearer alice-token" http://localhost:3000/api/v1/tasks   # miss
//	curl -H "Authorization: Bearer alice-token" http://localhost:3000/api/v1/tasks   # cache hit
//	curl -H "Authorization: Bearer bob-token"   http://localhost:3000/api/v1/tasks   # separate KeyBy bucket → miss
//
//	# Run wires the production-ops bundle: panic recovery, k8s
//	# health check, structured access log, Prometheus metrics.
//	curl http://localhost:3000/healthz       # 200 ok, no auth needed
//	curl http://localhost:3000/metrics       # Prometheus text format
//
//	# OpenAPI 3.0 spec generated from routes.yaml + typed handler
//	# schemas (see Engine.Add(...) wiring below).
//	curl -H "Authorization: Bearer alice-token" http://localhost:3000/openapi.json
package main

import (
	"embed"
	"log/slog"
	"net/http"
	"os"
	"sync"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/theizzatbek/fibermap"
	"github.com/theizzatbek/fibermap/examples/tasks/internal/admin"
	"github.com/theizzatbek/fibermap/examples/tasks/internal/appctx"
	"github.com/theizzatbek/fibermap/examples/tasks/internal/auth"
	"github.com/theizzatbek/fibermap/examples/tasks/internal/tasks"
	"github.com/theizzatbek/fibermap/openapi"
)

// Embed routes.yaml into the binary so the deploy is a single artifact.
// Showcases Run + WithRoutesFS.
//
//go:embed routes.yaml
var routesFS embed.FS

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// --- Engine setup.
	// Default[T]() returns an Engine with the v0.5 production ops bundle
	// pre-wired into Run (Recover + RequestID + RequestLogger +
	// HealthCheck + Metrics). New[T]() would give us the same bundle
	// minus the Prometheus /metrics endpoint.
	store := tasks.NewMemStore()
	valid := validator.New(validator.WithRequiredStructEnabled())
	eng := fibermap.Default[appctx.AppCtx]()

	eng.SetContextBuilder(func(c *fiber.Ctx) (appctx.AppCtx, error) {
		// All four locals are populated by the Fiber-level middlewares
		// wired via WithUse below. If any are missing, the request is
		// broken — Bearer() returns 401 before we ever get here.
		uid, _ := c.Locals("user_id").(string)
		role, _ := c.Locals("role").(string)
		rid, _ := c.Locals(fibermap.LocalsRequestID).(string)
		return appctx.AppCtx{
			UserID:    uid,
			Role:      role,
			RequestID: rid,
			Log:       logger.With("user_id", uid, "request_id", rid),
		}, nil
	})

	eng.RegisterMiddlewareFactory("require_role", auth.RequireRole)

	// Engine-wide cache defaults. The built-in cache (declared per-route
	// in routes.yaml via `cache: ...`) uses these for storage + key
	// partitioning. Default storage is Fiber's in-process map — fine
	// for this single-instance demo; in production set
	// `Storage: redis.New(...)` (from gofiber/storage) so replicas
	// share one cache.
	//
	// KeyBy is the critical part: without it, alice's cached GET /tasks
	// body would be served to bob. We scope by UserID so each caller
	// has their own cache namespace.
	eng.SetCacheDefaults(fibermap.CacheDefaults[appctx.AppCtx]{
		KeyBy: func(c *appctx.Ctx) string { return c.Data.UserID },
	})

	taskH := tasks.New(store, valid)
	eng.RegisterHandler("tasks.list", taskH.List)
	eng.RegisterHandler("tasks.get", taskH.Get)
	eng.RegisterHandler("tasks.create", taskH.Create)
	eng.RegisterHandler("tasks.update", taskH.Update)
	eng.RegisterHandler("tasks.delete", taskH.Delete)
	eng.RegisterHandler("admin.routes", admin.Routes(eng))

	// --- OpenAPI 3.0 spec — generated from Engine.Routes() + typed
	// per-handler schemas, served at /openapi.json. The generator
	// reads from the engine, so the spec is always in sync with the
	// live route table; we cache the JSON behind a sync.Once so each
	// scrape is a memcpy, not a re-reflection.
	gen := openapi.NewGenerator(eng,
		openapi.WithInfo(openapi.Info{
			Title:       "Tasks API",
			Version:     "0.1.0",
			Description: "Per-user task lists — demo for the fibermap library.",
		}),
		openapi.WithServer("http://localhost:3000", "local dev"),
		openapi.WithSecurity("BearerAuth", openapi.HTTPBearer()),
		openapi.MapMiddlewareToSecurity("auth", "BearerAuth"),
	)
	gen.OnHandler("tasks.create").
		Summary("Create a task").
		Body(tasks.CreateReq{}).
		Response(201, tasks.Task{}).
		Response(400, fiber.Map{"error": ""})
	gen.OnHandler("tasks.update").
		Summary("Update a task (partial)").
		Body(tasks.UpdateReq{}).
		Response(200, tasks.Task{}).
		Response(400, fiber.Map{"error": ""}).
		Response(404, fiber.Map{"error": ""})
	gen.OnHandler("tasks.get").
		Summary("Get one task").
		Response(200, tasks.Task{}).
		Response(404, fiber.Map{"error": ""})
	gen.OnHandler("tasks.list").
		Summary("List the caller's tasks").
		Response(200, fiber.Map{"tasks": []tasks.Task{}})
	gen.OnHandler("tasks.delete").
		Summary("Delete a task (admin only)").
		Response(204, nil).
		Response(403, fiber.Map{"error": ""})

	var (
		specOnce  sync.Once
		specJSON  []byte
		specErr   error
	)
	eng.Add("GET", "/openapi.json", "openapi.spec",
		func(c *fibermap.Context[appctx.AppCtx]) error {
			specOnce.Do(func() { specJSON, specErr = gen.Generate() })
			if specErr != nil {
				c.Data.Log.Error("openapi generation failed", "err", specErr)
				return c.Status(http.StatusInternalServerError).
					JSON(fiber.Map{"error": "spec generation failed"})
			}
			c.Set("Content-Type", "application/json")
			return c.Send(specJSON)
		},
		fibermap.AddOpts{
			Description: "OpenAPI 3.0 specification for this API",
			Tags:        []string{"meta"},
		},
	)

	// Run covers everything a production service typically needs.
	// The ops bundle (Recover, RequestID, RequestLogger, HealthCheck)
	// is on by default; Default[T] above added the Prometheus
	// /metrics endpoint on top.
	//
	// What's left to wire explicitly:
	//   - fiber.New with a custom ErrorHandler (slog-aware)
	//   - WithRecover(logger) — supply the structured logger instead
	//     of the default's slog.Default() so panic logs land in our
	//     JSON stream alongside everything else
	//   - WithRequestLogger(logger, ...) — same reason
	//   - WithUse(auth.Bearer()) — auth installed AFTER the default
	//     RequestID, BEFORE the ContextBuilder
	//   - WithRoutesFS(routesFS) — load YAML from the embedded FS
	logger.Info("listening", "addr", ":3000")
	err := eng.Run(
		fibermap.WithFiberConfig(fiber.Config{
			DisableStartupMessage: true,
			ErrorHandler: func(c *fiber.Ctx, err error) error {
				logger.Error("unhandled error", "err", err, "path", c.Path())
				return c.Status(http.StatusInternalServerError).
					JSON(fiber.Map{"error": "internal server error"})
			},
		}),
		fibermap.WithRecover(logger),
		fibermap.WithRequestLogger(logger, "/healthz", "/metrics"),
		fibermap.WithUse(auth.Bearer()),
		fibermap.WithRoutesFS(routesFS),
	)
	if err != nil {
		logger.Error("server stopped with error", "err", err)
		os.Exit(1)
	}
	logger.Info("bye")
}
