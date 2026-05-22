package main

import (
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/theizzatbek/fibermap"
	"github.com/theizzatbek/fibermap/examples/tasks/internal/admin"
	"github.com/theizzatbek/fibermap/examples/tasks/internal/appctx"
	"github.com/theizzatbek/fibermap/examples/tasks/internal/auth"
	"github.com/theizzatbek/fibermap/examples/tasks/internal/tasks"
	"github.com/theizzatbek/fibermap/fibermaptest"
)

// buildEngine wires the same engine main() does, minus the Fiber-level
// auth/request_id middlewares — tests for the engine surface (routes,
// chain, introspection) don't need real requests.
func buildEngine(t *testing.T) *fibermap.Engine[appctx.AppCtx] {
	t.Helper()
	store := tasks.NewMemStore()
	logger := slog.New(slog.NewTextHandler(testWriter{t}, nil))

	eng := fibermap.New[appctx.AppCtx]()
	eng.SetContextBuilder(func(c *fiber.Ctx) (appctx.AppCtx, error) {
		return appctx.AppCtx{Log: logger}, nil
	})
	eng.RegisterMiddlewareFactory("require_role", auth.RequireRole)

	taskH := tasks.New(store)
	eng.RegisterHandler("tasks.list", taskH.List)
	eng.RegisterHandler("tasks.get", taskH.Get)
	eng.RegisterHandler("tasks.create", taskH.Create)
	eng.RegisterHandler("tasks.update", taskH.Update)
	eng.RegisterHandler("tasks.delete", taskH.Delete)
	eng.RegisterHandler("admin.routes", admin.Routes(eng))

	if err := eng.LoadFS(routesFS, "routes.yaml"); err != nil {
		t.Fatal(err)
	}
	if err := eng.Mount(fiber.New()); err != nil {
		t.Fatal(err)
	}
	return eng
}

// TestRoutesYAML uses fibermaptest to assert the live routes.yaml
// declares exactly what we expect — no manual table maintenance.
func TestRoutesYAML(t *testing.T) {
	eng := buildEngine(t)

	fibermaptest.AssertRouteCount(t, eng, 6)

	fibermaptest.AssertRoute(t, eng, "GET", "/api/v1/tasks",
		fibermaptest.WithHandler("tasks.list"),
		fibermaptest.WithTags("tasks", "read"))

	fibermaptest.AssertRoute(t, eng, "POST", "/api/v1/tasks",
		fibermaptest.WithHandler("tasks.create"))

	// DELETE must require admin role — this is the contract we never
	// want to silently lose.
	fibermaptest.AssertRoute(t, eng, "DELETE", "/api/v1/tasks/:id",
		fibermaptest.WithHandler("tasks.delete"),
		fibermaptest.WithMiddleware("require_role"))

	// /admin/* lives behind the same role guard.
	fibermaptest.AssertRoute(t, eng, "GET", "/api/v1/admin/routes",
		fibermaptest.WithHandler("admin.routes"),
		fibermaptest.WithMiddleware("require_role"))

	// No accidentally-exposed paths.
	fibermaptest.AssertNoRoute(t, eng, "DELETE", "/api/v1/admin/routes")
	fibermaptest.AssertNoRoute(t, eng, "POST", "/api/v1/admin/routes")
}

// TestCreateThenList exercises the live HTTP stack end-to-end via
// Fiber's in-process Test helper — proves the wiring works without
// binding a port.
func TestCreateThenList(t *testing.T) {
	app := fiber.New()
	app.Use(auth.Bearer())

	store := tasks.NewMemStore()
	logger := slog.New(slog.NewTextHandler(testWriter{t}, nil))
	eng := fibermap.New[appctx.AppCtx]()
	eng.SetContextBuilder(func(c *fiber.Ctx) (appctx.AppCtx, error) {
		uid, _ := c.Locals("user_id").(string)
		role, _ := c.Locals("role").(string)
		return appctx.AppCtx{UserID: uid, Role: role, Log: logger}, nil
	})
	eng.RegisterMiddlewareFactory("require_role", auth.RequireRole)
	taskH := tasks.New(store)
	eng.RegisterHandler("tasks.list", taskH.List)
	eng.RegisterHandler("tasks.get", taskH.Get)
	eng.RegisterHandler("tasks.create", taskH.Create)
	eng.RegisterHandler("tasks.update", taskH.Update)
	eng.RegisterHandler("tasks.delete", taskH.Delete)
	eng.RegisterHandler("admin.routes", admin.Routes(eng))
	if err := eng.LoadFS(routesFS, "routes.yaml"); err != nil {
		t.Fatal(err)
	}
	if err := eng.Mount(app); err != nil {
		t.Fatal(err)
	}

	// 401 without a token.
	resp, _ := app.Test(httptest.NewRequest("GET", "/api/v1/tasks", nil))
	if resp.StatusCode != 401 {
		t.Errorf("no token: status = %d, want 401", resp.StatusCode)
	}

	// 201 on create.
	req := httptest.NewRequest("POST", "/api/v1/tasks", strings.NewReader(`{"title":"buy milk"}`))
	req.Header.Set("Authorization", "Bearer alice-token")
	req.Header.Set("Content-Type", "application/json")
	resp, _ = app.Test(req)
	if resp.StatusCode != 201 {
		t.Errorf("create: status = %d, want 201", resp.StatusCode)
	}

	// 400 on empty title.
	req = httptest.NewRequest("POST", "/api/v1/tasks", strings.NewReader(`{"title":""}`))
	req.Header.Set("Authorization", "Bearer alice-token")
	req.Header.Set("Content-Type", "application/json")
	resp, _ = app.Test(req)
	if resp.StatusCode != 400 {
		t.Errorf("empty title: status = %d, want 400", resp.StatusCode)
	}

	// 403 when user tries to DELETE.
	req = httptest.NewRequest("DELETE", "/api/v1/tasks/nope", nil)
	req.Header.Set("Authorization", "Bearer alice-token")
	resp, _ = app.Test(req)
	if resp.StatusCode != 403 {
		t.Errorf("user delete: status = %d, want 403", resp.StatusCode)
	}
}

// testWriter adapts *testing.T into io.Writer so slog output lands in
// test logs instead of stdout.
type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}
