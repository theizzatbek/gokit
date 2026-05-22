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
package main

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/theizzatbek/fibermap"
	"github.com/theizzatbek/fibermap/examples/tasks/internal/admin"
	"github.com/theizzatbek/fibermap/examples/tasks/internal/appctx"
	"github.com/theizzatbek/fibermap/examples/tasks/internal/auth"
	"github.com/theizzatbek/fibermap/examples/tasks/internal/tasks"
)

// Embed routes.yaml into the binary so the deploy is a single artifact.
// Showcases Engine.LoadFS.
//
//go:embed routes.yaml
var routesFS embed.FS

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	app := fiber.New(fiber.Config{
		// Quiet Fiber's banner — we log via slog.
		DisableStartupMessage: true,
		// Pass handler errors through to our central handler.
		ErrorHandler: func(c *fiber.Ctx, err error) error {
			logger.Error("unhandled error", "err", err, "path", c.Path())
			return c.Status(http.StatusInternalServerError).
				JSON(fiber.Map{"error": "internal server error"})
		},
	})

	// --- Fiber-level middlewares (run BEFORE fibermap's ContextBuilder).
	// Order matters: request_id provides the value the ContextBuilder
	// needs; auth provides user_id / role.
	app.Use(requestIDMiddleware)
	app.Use(auth.Bearer())

	// --- Engine setup.
	store := tasks.NewMemStore()
	valid := validator.New(validator.WithRequiredStructEnabled())
	eng := fibermap.New[appctx.AppCtx]()

	eng.SetContextBuilder(func(c *fiber.Ctx) (appctx.AppCtx, error) {
		// All four locals are populated by the Fiber-level middlewares
		// above. If any are missing, the whole request is broken — we
		// rely on Bearer() returning 401 before we ever get here.
		uid, _ := c.Locals("user_id").(string)
		role, _ := c.Locals("role").(string)
		rid, _ := c.Locals("request_id").(string)
		return appctx.AppCtx{
			UserID:    uid,
			Role:      role,
			RequestID: rid,
			Log:       logger.With("user_id", uid, "request_id", rid),
		}, nil
	})

	eng.RegisterMiddlewareFactory("require_role", auth.RequireRole)

	taskH := tasks.New(store, valid)
	eng.RegisterHandler("tasks.list", taskH.List)
	eng.RegisterHandler("tasks.get", taskH.Get)
	eng.RegisterHandler("tasks.create", taskH.Create)
	eng.RegisterHandler("tasks.update", taskH.Update)
	eng.RegisterHandler("tasks.delete", taskH.Delete)
	eng.RegisterHandler("admin.routes", admin.Routes(eng))

	if err := eng.LoadFS(routesFS, "routes.yaml"); err != nil {
		logger.Error("LoadFS failed", "err", err)
		os.Exit(1)
	}
	if err := eng.Mount(app); err != nil {
		logger.Error("Mount failed", "err", err)
		os.Exit(1)
	}

	// --- Graceful shutdown. Catch SIGINT/SIGTERM, give Fiber 10s to
	// drain in-flight requests, then exit.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		logger.Info("shutting down — draining for up to 10s")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = app.ShutdownWithContext(shutdownCtx)
	}()

	logger.Info("listening", "addr", ":3000", "routes", len(eng.Routes()))
	if err := app.Listen(":3000"); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("listen failed", "err", err)
		os.Exit(1)
	}
	logger.Info("bye")
}

// requestIDMiddleware reads X-Request-ID from the incoming request or
// generates one, sets it on Locals (for ContextBuilder) and echoes it
// back in the response header (so callers can correlate).
func requestIDMiddleware(c *fiber.Ctx) error {
	id := c.Get("X-Request-ID")
	if id == "" {
		var b [8]byte
		_, _ = rand.Read(b[:])
		id = hex.EncodeToString(b[:])
	}
	c.Locals("request_id", id)
	c.Set("X-Request-ID", id)
	return c.Next()
}
