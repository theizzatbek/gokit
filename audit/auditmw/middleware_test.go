package auditmw_test

import (
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"

	"github.com/theizzatbek/gokit/audit"
	"github.com/theizzatbek/gokit/audit/auditmw"
	xerrs "github.com/theizzatbek/gokit/errs"
)

func newLoggerWithStore(t *testing.T) (*audit.Logger, *audit.MemoryStore) {
	t.Helper()
	store := audit.NewMemoryStore()
	l, err := audit.New(store, audit.Config{ServiceName: "test"})
	if err != nil {
		t.Fatalf("audit.New: %v", err)
	}
	return l, store
}

func TestMiddleware_LogsPOSTByDefault(t *testing.T) {
	l, store := newLoggerWithStore(t)
	app := fiber.New()
	app.Use(auditmw.Middleware(l, auditmw.WithSubject(func(c *fiber.Ctx) string {
		return c.Get("X-Test-User")
	})))
	app.Post("/tasks", func(c *fiber.Ctx) error {
		return c.Status(fiber.StatusCreated).SendString("ok")
	})

	req := httptest.NewRequest("POST", "/tasks", nil)
	req.Header.Set("X-Test-User", "u-42")
	_, err := app.Test(req)
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	events := store.Snapshot()
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	e := events[0]
	if e.Action != "POST./tasks" {
		t.Errorf("Action = %q, want POST./tasks", e.Action)
	}
	if e.Actor.Subject != "u-42" {
		t.Errorf("Actor.Subject = %q, want u-42", e.Actor.Subject)
	}
	if e.Outcome != audit.Success {
		t.Errorf("Outcome = %q, want success", e.Outcome)
	}
	if e.Target.Type != "tasks" {
		t.Errorf("Target.Type = %q, want tasks", e.Target.Type)
	}
}

func TestMiddleware_SkipsGETByDefault(t *testing.T) {
	l, store := newLoggerWithStore(t)
	app := fiber.New()
	app.Use(auditmw.Middleware(l))
	app.Get("/tasks", func(c *fiber.Ctx) error { return c.SendString("ok") })
	_, _ = app.Test(httptest.NewRequest("GET", "/tasks", nil))
	if got := len(store.Snapshot()); got != 0 {
		t.Errorf("GET-snapshot len = %d, want 0", got)
	}
}

func TestMiddleware_IncludesGETWhenConfigured(t *testing.T) {
	l, store := newLoggerWithStore(t)
	app := fiber.New()
	app.Use(auditmw.Middleware(l, auditmw.WithIncludeMethods("GET")))
	app.Get("/tasks", func(c *fiber.Ctx) error { return c.SendString("ok") })
	_, _ = app.Test(httptest.NewRequest("GET", "/tasks", nil))
	if got := len(store.Snapshot()); got != 1 {
		t.Errorf("GET-snapshot len = %d, want 1 (override)", got)
	}
}

func TestMiddleware_SkipPathsExcludesHealthChecks(t *testing.T) {
	l, store := newLoggerWithStore(t)
	app := fiber.New()
	app.Use(auditmw.Middleware(l, auditmw.WithSkipPaths("/healthz")))
	app.Post("/healthz", func(c *fiber.Ctx) error { return c.SendString("ok") })
	_, _ = app.Test(httptest.NewRequest("POST", "/healthz", nil))
	if got := len(store.Snapshot()); got != 0 {
		t.Errorf("/healthz emitted event; want skipped")
	}
}

func TestMiddleware_OutcomeFromStatus(t *testing.T) {
	cases := []struct {
		name   string
		status int
		want   audit.Outcome
	}{
		{"2xx → success", fiber.StatusCreated, audit.Success},
		{"401 → denied", fiber.StatusUnauthorized, audit.Denied},
		{"403 → denied", fiber.StatusForbidden, audit.Denied},
		{"400 → failure", fiber.StatusBadRequest, audit.Failure},
		{"500 → failure", fiber.StatusInternalServerError, audit.Failure},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l, store := newLoggerWithStore(t)
			app := fiber.New()
			app.Use(auditmw.Middleware(l))
			app.Post("/x", func(ctx *fiber.Ctx) error {
				return ctx.SendStatus(c.status)
			})
			_, _ = app.Test(httptest.NewRequest("POST", "/x", nil))
			evs := store.Snapshot()
			if len(evs) != 1 {
				t.Fatalf("events = %d", len(evs))
			}
			if evs[0].Outcome != c.want {
				t.Errorf("Outcome = %q, want %q", evs[0].Outcome, c.want)
			}
		})
	}
}

func TestMiddleware_TargetExtractsIDParam(t *testing.T) {
	l, store := newLoggerWithStore(t)
	app := fiber.New()
	app.Use(auditmw.Middleware(l))
	app.Put("/tasks/:id", func(c *fiber.Ctx) error { return c.SendStatus(200) })
	_, _ = app.Test(httptest.NewRequest("PUT", "/tasks/42", nil))
	evs := store.Snapshot()
	if len(evs) != 1 {
		t.Fatalf("events = %d", len(evs))
	}
	if evs[0].Target.Type != "tasks" || evs[0].Target.ID != "42" {
		t.Errorf("Target = %+v, want {tasks 42}", evs[0].Target)
	}
}

func TestMiddleware_NilLoggerNoOp(t *testing.T) {
	app := fiber.New()
	app.Use(auditmw.Middleware(nil))
	app.Post("/x", func(c *fiber.Ctx) error { return c.SendStatus(200) })
	resp, err := app.Test(httptest.NewRequest("POST", "/x", nil))
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200 (nil logger no-op)", resp.StatusCode)
	}
}

func TestMiddleware_CustomActionAndMetadata(t *testing.T) {
	l, store := newLoggerWithStore(t)
	app := fiber.New()
	app.Use(auditmw.Middleware(l,
		auditmw.WithAction(func(c *fiber.Ctx) string { return "billing.invoice_paid" }),
		auditmw.WithMetadata(func(c *fiber.Ctx) map[string]any {
			return map[string]any{"tenant": "acme"}
		}),
	))
	app.Post("/x", func(c *fiber.Ctx) error { return c.SendStatus(200) })
	_, _ = app.Test(httptest.NewRequest("POST", "/x", nil))
	evs := store.Snapshot()
	if len(evs) != 1 {
		t.Fatalf("events = %d", len(evs))
	}
	if evs[0].Action != "billing.invoice_paid" {
		t.Errorf("Action = %q", evs[0].Action)
	}
	if evs[0].Metadata["tenant"] != "acme" {
		t.Errorf("Metadata.tenant = %v, want acme", evs[0].Metadata["tenant"])
	}
}

// errsErrorHandler mirrors fibermap.ErrorHandler: it maps *errs.Error
// to an HTTP status AFTER the middleware chain unwinds — the shape
// every gokit service uses. auditmw must classify by the returned
// error, not by the in-flight status (still 200 at middleware time).
func errsErrorHandler(c *fiber.Ctx, err error) error {
	var e *xerrs.Error
	if errors.As(err, &e) {
		status, body := xerrs.HTTP(err)
		if status == 0 {
			status = fiber.StatusInternalServerError
		}
		return c.Status(status).JSON(body)
	}
	return fiber.DefaultErrorHandler(c, err)
}

func TestMiddleware_DeniedWhenHandlerReturnsPermissionError(t *testing.T) {
	l, store := newLoggerWithStore(t)
	app := fiber.New(fiber.Config{ErrorHandler: errsErrorHandler})
	app.Use(auditmw.Middleware(l))
	app.Post("/admin/keys", func(c *fiber.Ctx) error {
		return xerrs.Permission("missing_role", "requires ADMIN")
	})

	resp, err := app.Test(httptest.NewRequest("POST", "/admin/keys", nil))
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if resp.StatusCode != fiber.StatusForbidden {
		t.Fatalf("wire status = %d, want 403", resp.StatusCode)
	}
	events := store.Snapshot()
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	e := events[0]
	if e.Outcome != audit.Denied {
		t.Errorf("Outcome = %q, want denied", e.Outcome)
	}
	if e.Metadata["error"] != "missing_role" {
		t.Errorf(`Metadata["error"] = %v, want "missing_role"`, e.Metadata["error"])
	}
	if v, ok := e.Metadata["status"]; ok {
		t.Errorf(`Metadata["status"] = %v present — must be omitted when the handler errored`, v)
	}
}

func TestMiddleware_FailureWhenHandlerReturnsOpaqueError(t *testing.T) {
	l, store := newLoggerWithStore(t)
	app := fiber.New(fiber.Config{ErrorHandler: errsErrorHandler})
	app.Use(auditmw.Middleware(l))
	app.Post("/tasks", func(c *fiber.Ctx) error {
		return errors.New("boom")
	})
	if _, err := app.Test(httptest.NewRequest("POST", "/tasks", nil)); err != nil {
		t.Fatalf("Test: %v", err)
	}
	events := store.Snapshot()
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	if events[0].Outcome != audit.Failure {
		t.Errorf("Outcome = %q, want failure", events[0].Outcome)
	}
	if events[0].Metadata["error"] != "boom" {
		t.Errorf(`Metadata["error"] = %v, want "boom"`, events[0].Metadata["error"])
	}
}

func TestMiddleware_ManualStatusStillClassified(t *testing.T) {
	l, store := newLoggerWithStore(t)
	app := fiber.New()
	app.Use(auditmw.Middleware(l))
	app.Post("/manual", func(c *fiber.Ctx) error {
		return c.SendStatus(fiber.StatusForbidden) // hand-written status, nil error
	})
	if _, err := app.Test(httptest.NewRequest("POST", "/manual", nil)); err != nil {
		t.Fatalf("Test: %v", err)
	}
	events := store.Snapshot()
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	if events[0].Outcome != audit.Denied {
		t.Errorf("Outcome = %q, want denied (status 403, nil error)", events[0].Outcome)
	}
	if events[0].Metadata["status"] != 403 {
		t.Errorf(`Metadata["status"] = %v, want 403`, events[0].Metadata["status"])
	}
}
