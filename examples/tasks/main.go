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
//	# Bearer:
//	curl -H "Authorization: Bearer alice-token"            http://localhost:3000/api/v1/tasks
//	# Basic (alternative — see auth.BearerOrBasic):
//	curl -u alice:secret                                   http://localhost:3000/api/v1/tasks
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
//	# Configuration is env-driven — see .env.example for every knob
//	# (ADDR, LOG_LEVEL, CORS_ORIGINS, RATE_LIMIT_MAX, ...). With no env,
//	# defaults match the curl examples above.
//
//	# Run wires the production-ops bundle: panic recovery, k8s
//	# health check, structured access log, Prometheus metrics, plus
//	# helmet (security headers) + cors + per-IP rate limiting.
//	curl http://localhost:3000/healthz       # 200 ok, no auth needed
//	curl http://localhost:3000/metrics       # Prometheus text format
//
//	# OpenAPI 3.0 spec generated from routes.yaml + typed handler
//	# schemas. Public — no auth required.
//	curl http://localhost:3000/openapi.json
//
//	# Browsable HTML docs (Scalar API Reference, loaded from CDN).
//	# Public — open in browser.
//	open http://localhost:3000/docs
package main

import (
	"embed"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/helmet"
	"github.com/gofiber/fiber/v2/middleware/limiter"
	"github.com/theizzatbek/fibermap"
	"github.com/theizzatbek/fibermap/examples/tasks/internal/admin"
	"github.com/theizzatbek/fibermap/examples/tasks/internal/appctx"
	"github.com/theizzatbek/fibermap/examples/tasks/internal/auth"
	"github.com/theizzatbek/fibermap/examples/tasks/internal/config"
	"github.com/theizzatbek/fibermap/examples/tasks/internal/tasks"
	"github.com/theizzatbek/fibermap/openapi"
)

// Embed routes.yaml into the binary so the deploy is a single artifact.
// Showcases Run + WithRoutesFS.
//
//go:embed routes.yaml
var routesFS embed.FS

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	logger, err := newLogger(cfg.LogFormat, cfg.LogLevel)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

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

	fibermap.RegisterMiddlewareFactory(eng, "require_role", auth.RequireRole)

	// Engine-wide validator — fibermap.RegisterHandlerWithBody and friends pass
	// the parsed struct through it before calling the handler.
	eng.SetValidator(valid)

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

	// Body-binding handlers use fibermap.RegisterHandlerWithBody — the request
	// type appears once (in the handler signature) and is auto-parsed
	// + validated before the handler runs. The body schema is also
	// auto-attached for OpenAPI. Other handlers use the symmetric
	// fibermap.RegisterHandler — same shape, just no typed body.
	taskH := tasks.New(store, valid)
	fibermap.RegisterHandler(eng, "tasks.list", taskH.List,
		fibermap.WithResponse(fiber.StatusOK, tasks.ListResponse{}))
	fibermap.RegisterHandler(eng, "tasks.get", taskH.Get,
		fibermap.WithResponse(fiber.StatusOK, tasks.Task{}))
	fibermap.RegisterHandlerWithBody(eng, "tasks.create", taskH.Create,
		fibermap.WithResponse(fiber.StatusCreated, tasks.Task{}))
	fibermap.RegisterHandlerWithBody(eng, "tasks.update", taskH.Update,
		fibermap.WithResponse(fiber.StatusOK, tasks.Task{}))
	fibermap.RegisterHandler(eng, "tasks.delete", taskH.Delete,
		fibermap.WithResponse(fiber.StatusNoContent, nil))
	fibermap.RegisterHandler(eng, "admin.routes", admin.Routes(eng))

	// OpenAPI 3.0 spec — generated from Engine.Routes() + the handler
	// schemas attached above. The generator reads from the engine,
	// so the spec is always in sync with the live route table.
	// Every 4xx/5xx response is `tasks.ErrorResponse` (typed
	// `{"error": "..."}`), declared once on the generator so each
	// operation advertises the same error contract without per-handler
	// boilerplate.
	genOpts := []openapi.Option{
		openapi.WithInfo(openapi.Info{
			Title:       "Tasks API",
			Version:     "0.1.0",
			Description: "Per-user task lists — demo for the fibermap library.",
		}),
	}
	if cfg.APIBaseURL != "" {
		genOpts = append(genOpts, openapi.WithServer(cfg.APIBaseURL, cfg.Env))
	}
	genOpts = append(genOpts,
		openapi.WithDefaultResponse(fiber.StatusBadRequest, tasks.ErrorResponse{}),
		openapi.WithDefaultResponse(fiber.StatusUnauthorized, tasks.ErrorResponse{}),
		openapi.WithDefaultResponse(fiber.StatusForbidden, tasks.ErrorResponse{}),
		openapi.WithDefaultResponse(fiber.StatusNotFound, tasks.ErrorResponse{}),
		openapi.WithDefaultResponse(fiber.StatusInternalServerError, tasks.ErrorResponse{}),
		openapi.SecurityMapping("BearerAuth", openapi.HTTPBearer(), "auth"),
		openapi.SecurityMapping("BasicAuth", openapi.HTTPBasic(), "auth"),
	)
	gen := openapi.NewGenerator(eng, genOpts...)

	// Mount installs /openapi.json (sync.Once-cached spec) and /docs
	// (Scalar UI viewer) — both as programmatic routes on the
	// engine. Pass MountOpts to override paths or pick a different
	// viewer (openapi.SwaggerUI / openapi.Redoc).
	if err := gen.Mount(); err != nil {
		logger.Error("openapi.Mount failed", "err", err)
		os.Exit(1)
	}

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
	logger.Info("listening", "addr", cfg.Addr, "env", cfg.Env)
	err = eng.Run(
		fibermap.WithAddr(cfg.Addr),
		fibermap.WithShutdownTimeout(cfg.ShutdownTimeout),
		fibermap.WithFiberConfig(fiber.Config{
			DisableStartupMessage: true,
			BodyLimit:             cfg.BodyLimit,
			ErrorHandler: func(c *fiber.Ctx, err error) error {
				logger.Error("unhandled error", "err", err, "path", c.Path())
				return c.Status(http.StatusInternalServerError).
					JSON(tasks.ErrorResponse{Error: "internal server error"})
			},
		}),
		fibermap.WithRecover(logger),
		fibermap.WithRequestLogger(logger, "/healthz", "/metrics"),
		// Order matters: helmet sets security headers on every response
		// (including 401/429); cors before auth so OPTIONS preflight
		// doesn't trip on 401; limiter before auth so anonymous flood
		// doesn't pay for credential lookup; auth runs last so locals
		// are populated right before fibermap's ContextBuilder.
		fibermap.WithUse(
			helmet.New(),
			cors.New(cors.Config{
				AllowOrigins: strings.Join(cfg.CORSOrigins, ","),
				AllowMethods: strings.Join(cfg.CORSMethods, ","),
			}),
			limiter.New(limiter.Config{
				Max:        cfg.RateLimitMax,
				Expiration: cfg.RateLimitExpiration,
			}),
			auth.BearerOrBasic("/docs", "/openapi.json"),
		),
		fibermap.WithRoutesFS(routesFS),
	)
	if err != nil {
		logger.Error("server stopped with error", "err", err)
		os.Exit(1)
	}
	logger.Info("bye")
}

// newLogger builds a slog.Logger whose handler and level come from
// config. Format is text|json; level is one of debug|info|warn|error
// (validated in config.Load).
func newLogger(format, levelStr string) (*slog.Logger, error) {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(levelStr)); err != nil {
		return nil, fmt.Errorf("logger: parse level %q: %w", levelStr, err)
	}
	opts := &slog.HandlerOptions{Level: lvl}
	var h slog.Handler
	switch format {
	case "json":
		h = slog.NewJSONHandler(os.Stdout, opts)
	case "text":
		h = slog.NewTextHandler(os.Stdout, opts)
	default:
		return nil, fmt.Errorf("logger: unknown format %q", format)
	}
	return slog.New(h), nil
}
