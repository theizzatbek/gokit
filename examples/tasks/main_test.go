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

// newEngine builds the engine the way main() does, minus the
// Fiber-level middlewares — those are layered on inside setupApp when
// the test needs real HTTP behaviour. The engine is NOT mounted yet
// (Mount is one-shot).
func newEngine(t *testing.T) *fibermap.Engine[appctx.AppCtx] {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(testWriter{t}, nil))
	store := tasks.NewMemStore()
	valid := validator.New(validator.WithRequiredStructEnabled())

	eng := fibermap.New[appctx.AppCtx]()
	eng.SetContextBuilder(func(c *fiber.Ctx) (appctx.AppCtx, error) {
		uid, _ := c.Locals("user_id").(string)
		role, _ := c.Locals("role").(string)
		return appctx.AppCtx{UserID: uid, Role: role, Log: logger}, nil
	})
	fibermap.RegisterMiddlewareFactory(eng, "require_role", auth.RequireRole)
	eng.SetValidator(valid)
	eng.SetCacheDefaults(fibermap.CacheDefaults[appctx.AppCtx]{
		KeyBy: func(c *appctx.Ctx) string { return c.Data.UserID },
	})

	taskH := tasks.New(store, valid)
	fibermap.RegisterHandler(eng, "tasks.list", taskH.List)
	fibermap.RegisterHandler(eng, "tasks.get", taskH.Get)
	fibermap.RegisterHandlerWithBody(eng, "tasks.create", taskH.Create)
	fibermap.RegisterHandlerWithBody(eng, "tasks.update", taskH.Update)
	fibermap.RegisterHandler(eng, "tasks.delete", taskH.Delete)
	fibermap.RegisterHandler(eng, "admin.routes", admin.Routes(eng))

	if err := eng.LoadFS(routesFS, "routes.yaml"); err != nil {
		t.Fatal(err)
	}
	return eng
}

// setupEngine is for introspection-only tests: the engine is mounted
// on a throwaway fiber.App so Routes() is populated, but no HTTP
// requests are issued.
func setupEngine(t *testing.T) *fibermap.Engine[appctx.AppCtx] {
	t.Helper()
	eng := newEngine(t)
	if err := eng.Mount(fiber.New()); err != nil {
		t.Fatal(err)
	}
	return eng
}

// setupApp is for HTTP tests: an app with auth.Bearer() in front of
// the engine, ready for app.Test().
func setupApp(t *testing.T) *fiber.App {
	t.Helper()
	app := fiber.New()
	app.Use(auth.Bearer())
	eng := newEngine(t)
	if err := eng.Mount(app); err != nil {
		t.Fatal(err)
	}
	return app
}

// TestRoutesYAML uses fibermaptest to assert the live routes.yaml
// declares exactly what we expect — no manual table maintenance.
func TestRoutesYAML(t *testing.T) {
	eng := setupEngine(t)

	fibermaptest.AssertRouteCount(t, eng, 6)
	fibermaptest.AssertRoute(t, eng, "GET", "/api/v1/tasks",
		fibermaptest.WithHandler("tasks.list"),
		fibermaptest.WithTags("tasks"))
	fibermaptest.AssertRoute(t, eng, "POST", "/api/v1/tasks",
		fibermaptest.WithHandler("tasks.create"))
	// DELETE must require admin role — contract we never want to lose silently.
	fibermaptest.AssertRoute(t, eng, "DELETE", "/api/v1/tasks/:id",
		fibermaptest.WithHandler("tasks.delete"),
		fibermaptest.WithMiddleware("require_role"))
	// /admin/* lives behind the same role guard.
	fibermaptest.AssertRoute(t, eng, "GET", "/api/v1/admin/routes",
		fibermaptest.WithHandler("admin.routes"),
		fibermaptest.WithMiddleware("require_role"))
	fibermaptest.AssertNoRoute(t, eng, "DELETE", "/api/v1/admin/routes")
	fibermaptest.AssertNoRoute(t, eng, "POST", "/api/v1/admin/routes")
}

func TestCreateThenList(t *testing.T) {
	app := setupApp(t)

	resp, _ := app.Test(httptest.NewRequest("GET", "/api/v1/tasks", nil))
	if resp.StatusCode != 401 {
		t.Errorf("no token: status = %d, want 401", resp.StatusCode)
	}

	req := httptest.NewRequest("POST", "/api/v1/tasks", strings.NewReader(`{"title":"buy milk"}`))
	req.Header.Set("Authorization", "Bearer alice-token")
	req.Header.Set("Content-Type", "application/json")
	resp, _ = app.Test(req)
	if resp.StatusCode != 201 {
		t.Errorf("create: status = %d, want 201", resp.StatusCode)
	}

	req = httptest.NewRequest("POST", "/api/v1/tasks", strings.NewReader(`{"title":""}`))
	req.Header.Set("Authorization", "Bearer alice-token")
	req.Header.Set("Content-Type", "application/json")
	resp, _ = app.Test(req)
	if resp.StatusCode != 400 {
		t.Errorf("empty title: status = %d, want 400", resp.StatusCode)
	}

	req = httptest.NewRequest("DELETE", "/api/v1/tasks/nope", nil)
	req.Header.Set("Authorization", "Bearer alice-token")
	resp, _ = app.Test(req)
	if resp.StatusCode != 403 {
		t.Errorf("user delete: status = %d, want 403", resp.StatusCode)
	}
}

func TestCacheIsolatesByUser(t *testing.T) {
	app := setupApp(t)

	type listResp struct {
		Tasks []map[string]any `json:"tasks"`
	}
	list := func(token string) []map[string]any {
		req := httptest.NewRequest("GET", "/api/v1/tasks", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, _ := app.Test(req)
		body, _ := io.ReadAll(resp.Body)
		var v listResp
		if err := json.Unmarshal(body, &v); err != nil {
			t.Fatalf("decode (token=%s): %v (raw=%q)", token, err, string(body))
		}
		return v.Tasks
	}

	// 1. Alice's first GET — populates the cache.
	if got := list("alice-token"); len(got) != 0 {
		t.Fatalf("alice initial list = %v, want []", got)
	}

	// 2. Alice creates a task; the cached GET would otherwise show it.
	create := httptest.NewRequest("POST", "/api/v1/tasks", strings.NewReader(`{"title":"buy milk"}`))
	create.Header.Set("Authorization", "Bearer alice-token")
	create.Header.Set("Content-Type", "application/json")
	if resp, _ := app.Test(create); resp.StatusCode != 201 {
		t.Fatalf("create: status = %d", resp.StatusCode)
	}

	// 3. Alice's second GET — should still return [] because of the
	//    cache hit from step 1. Proves cache is hot, scoped to alice.
	if got := list("alice-token"); len(got) != 0 {
		t.Errorf("alice cached list = %v, want [] (cache miss → KeyBy not wired)", got)
	}

	// 4. Bob's GET — separate KeyBy bucket, handler runs fresh, sees
	//    no tasks owned by bob. Proves cache namespace is per-user.
	if got := list("bob-token"); len(got) != 0 {
		t.Errorf("bob list = %v, want []", got)
	}
}

// testWriter adapts *testing.T into io.Writer so slog output lands in
// test logs instead of stdout.
type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}
