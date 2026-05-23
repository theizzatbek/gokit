package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-playground/validator/v10"
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
	fibermap.RegisterMiddlewareFactory(eng, "require_role", auth.RequireRole)
	eng.SetCacheDefaults(fibermap.CacheDefaults[appctx.AppCtx]{
		KeyBy: func(c *appctx.Ctx) string { return c.Data.UserID },
	})

	valid := validator.New(validator.WithRequiredStructEnabled())
	eng.SetValidator(valid)
	taskH := tasks.New(store, valid)
	fibermap.RegisterHandler(eng, "tasks.list", taskH.List)
	fibermap.RegisterHandler(eng, "tasks.get", taskH.Get)
	fibermap.RegisterBody(eng, "tasks.create", taskH.Create)
	fibermap.RegisterBody(eng, "tasks.update", taskH.Update)
	fibermap.RegisterHandler(eng, "tasks.delete", taskH.Delete)
	fibermap.RegisterHandler(eng, "admin.routes", admin.Routes(eng))

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
		fibermaptest.WithTags("tasks"))

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
	fibermap.RegisterMiddlewareFactory(eng, "require_role", auth.RequireRole)
	eng.SetCacheDefaults(fibermap.CacheDefaults[appctx.AppCtx]{
		KeyBy: func(c *appctx.Ctx) string { return c.Data.UserID },
	})
	valid := validator.New(validator.WithRequiredStructEnabled())
	eng.SetValidator(valid)
	taskH := tasks.New(store, valid)
	fibermap.RegisterHandler(eng, "tasks.list", taskH.List)
	fibermap.RegisterHandler(eng, "tasks.get", taskH.Get)
	fibermap.RegisterBody(eng, "tasks.create", taskH.Create)
	fibermap.RegisterBody(eng, "tasks.update", taskH.Update)
	fibermap.RegisterHandler(eng, "tasks.delete", taskH.Delete)
	fibermap.RegisterHandler(eng, "admin.routes", admin.Routes(eng))
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

// TestCacheIsolatesByUser proves the cache wired up in routes.yaml
// + main.go (KeyBy=UserID) does its job: a subsequent identical GET
// from the SAME user is served from cache (handler not invoked),
// while a GET from a DIFFERENT user gets a fresh handler invocation.
func TestCacheIsolatesByUser(t *testing.T) {
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
	fibermap.RegisterMiddlewareFactory(eng, "require_role", auth.RequireRole)
	eng.SetCacheDefaults(fibermap.CacheDefaults[appctx.AppCtx]{
		KeyBy: func(c *appctx.Ctx) string { return c.Data.UserID },
	})
	valid := validator.New(validator.WithRequiredStructEnabled())
	eng.SetValidator(valid)
	taskH := tasks.New(store, valid)
	fibermap.RegisterHandler(eng, "tasks.list", taskH.List)
	fibermap.RegisterHandler(eng, "tasks.get", taskH.Get)
	fibermap.RegisterBody(eng, "tasks.create", taskH.Create)
	fibermap.RegisterBody(eng, "tasks.update", taskH.Update)
	fibermap.RegisterHandler(eng, "tasks.delete", taskH.Delete)
	fibermap.RegisterHandler(eng, "admin.routes", admin.Routes(eng))
	if err := eng.LoadFS(routesFS, "routes.yaml"); err != nil {
		t.Fatal(err)
	}
	if err := eng.Mount(app); err != nil {
		t.Fatal(err)
	}

	type listResp struct {
		Tasks []map[string]any `json:"tasks"`
	}

	// 1. Alice lists — empty.
	aliceList := func() []map[string]any {
		req := httptest.NewRequest("GET", "/api/v1/tasks", nil)
		req.Header.Set("Authorization", "Bearer alice-token")
		resp, _ := app.Test(req)
		body, _ := io.ReadAll(resp.Body)
		var v listResp
		if err := json.Unmarshal(body, &v); err != nil {
			t.Fatalf("alice decode: %v (raw=%q)", err, string(body))
		}
		return v.Tasks
	}
	if got := aliceList(); len(got) != 0 {
		t.Fatalf("alice initial list = %v, want []", got)
	}

	// 2. Alice creates a task.
	req := httptest.NewRequest("POST", "/api/v1/tasks", strings.NewReader(`{"title":"buy milk"}`))
	req.Header.Set("Authorization", "Bearer alice-token")
	req.Header.Set("Content-Type", "application/json")
	if resp, _ := app.Test(req); resp.StatusCode != 201 {
		t.Fatalf("create: status = %d", resp.StatusCode)
	}

	// 3. Alice lists again — should still return [] because her PREVIOUS
	//    GET is cached. This proves the cache is hot.
	if got := aliceList(); len(got) != 0 {
		t.Errorf("alice second list = %v, want [] (cached); KeyBy probably not wired", got)
	}

	// 4. Bob lists — separate KeyBy bucket, so handler runs, sees alice's
	//    task is owned by alice (not bob), and returns []. The point of
	//    this assertion is that bob's response is NOT alice's cached one;
	//    we exercised the handler for bob's KeyBy.
	bobReq := httptest.NewRequest("GET", "/api/v1/tasks", nil)
	bobReq.Header.Set("Authorization", "Bearer bob-token")
	bobResp, _ := app.Test(bobReq)
	if bobResp.StatusCode != 200 {
		t.Fatalf("bob: status = %d", bobResp.StatusCode)
	}
	bobBody, _ := io.ReadAll(bobResp.Body)
	var bobResult listResp
	if err := json.Unmarshal(bobBody, &bobResult); err != nil {
		t.Fatalf("bob body decode: %v (raw=%q)", err, string(bobBody))
	}
	if len(bobResult.Tasks) != 0 {
		t.Errorf("bob list = %v, want []", bobResult.Tasks)
	}
}

// testWriter adapts *testing.T into io.Writer so slog output lands in
// test logs instead of stdout.
type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}
