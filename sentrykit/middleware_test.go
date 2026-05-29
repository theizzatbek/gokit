package sentrykit_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/gofiber/fiber/v2"

	xerrs "github.com/theizzatbek/gokit/errs"
	"github.com/theizzatbek/gokit/fibermap"
	"github.com/theizzatbek/gokit/sentrykit"
)

// captureSink installs a fresh Sentry client whose BeforeSend
// captures into a slice and returns nil (no transport). t.Cleanup
// flushes + resets the global hub so the next test starts clean.
type captureSink struct {
	mu     sync.Mutex
	events []*sentry.Event
}

func (s *captureSink) snapshot() []*sentry.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*sentry.Event, len(s.events))
	copy(out, s.events)
	return out
}

func newSinkClient(t *testing.T, opts ...sentrykit.Option) *captureSink {
	t.Helper()
	sink := &captureSink{}
	all := append([]sentrykit.Option{
		sentrykit.WithBeforeSend(func(e *sentry.Event, _ *sentry.EventHint) *sentry.Event {
			sink.mu.Lock()
			sink.events = append(sink.events, e)
			sink.mu.Unlock()
			return nil
		}),
	}, opts...)
	shutdown, err := sentrykit.Setup(context.Background(), testDSN, all...)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		_ = shutdown(ctx)
	})
	return sink
}

func TestFiberMiddleware_StoresHubOnLocals(t *testing.T) {
	_ = newSinkClient(t)

	app := fiber.New()
	app.Use(sentrykit.FiberMiddleware())
	var hub *sentry.Hub
	app.Get("/ping", func(c *fiber.Ctx) error {
		hub = sentrykit.HubFromContext(c)
		return c.SendString("ok")
	})

	if _, err := app.Test(httptest.NewRequest("GET", "/ping", nil)); err != nil {
		t.Fatal(err)
	}
	if hub == nil {
		t.Fatal("HubFromContext returned nil")
	}
	if hub == sentry.CurrentHub() {
		t.Error("expected per-request hub clone, got the process-global hub")
	}
}

func TestFiberMiddleware_CapturesPanicAndRePanics(t *testing.T) {
	sink := newSinkClient(t)

	app := fiber.New()
	// fibermap.Recover catches the re-panic and writes 500.
	app.Use(fibermap.Recover(nil))
	app.Use(sentrykit.FiberMiddleware())
	// Panic with an error value so sentry-go classifies it as an
	// Exception (string panics surface as Message events instead —
	// also captured, but with different event shape).
	bang := errors.New("kaboom")
	app.Get("/boom", func(c *fiber.Ctx) error { panic(bang) })

	resp, err := app.Test(httptest.NewRequest("GET", "/boom", nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 500 {
		t.Errorf("status = %d, want 500 (fibermap.Recover should still own the response)", resp.StatusCode)
	}

	events := sink.snapshot()
	if len(events) == 0 {
		t.Fatal("no Sentry event captured for panic")
	}
	// Sentry-go represents recovered panics under event.Exception.
	if len(events[0].Exception) == 0 {
		t.Errorf("expected event.Exception to be populated, got %+v", events[0])
	}
}

func TestFiberMiddleware_RequestScopeIncludesRouteAndRequestID(t *testing.T) {
	sink := newSinkClient(t)

	app := fiber.New()
	// Install fibermap's request-id middleware upstream so the
	// sentry scope can pick it up.
	app.Use(fibermap.RequestID())
	app.Use(sentrykit.FiberMiddleware())
	app.Get("/users/:id", func(c *fiber.Ctx) error {
		// Capture a message so a Sentry event ships with the current
		// (request-scoped) hub state.
		sentrykit.HubFromContext(c).CaptureMessage("hit /users/:id")
		return c.SendString(c.Params("id"))
	})

	req := httptest.NewRequest("GET", "/users/42", nil)
	req.Header.Set("X-Request-ID", "rid-abc")
	if _, err := app.Test(req); err != nil {
		t.Fatal(err)
	}

	events := sink.snapshot()
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	e := events[0]
	if got := e.Tags["http.route"]; got != "/users/:id" {
		t.Errorf("http.route tag = %q, want /users/:id", got)
	}
	if got := e.Tags["http.method"]; got != "GET" {
		t.Errorf("http.method tag = %q, want GET", got)
	}
	if got := e.Tags["request_id"]; got != "rid-abc" {
		t.Errorf("request_id tag = %q, want rid-abc", got)
	}
	if e.Request == nil || e.Request.Method != "GET" {
		t.Errorf("event.Request.Method = %v, want GET", e.Request)
	}
}

func TestWrapErrorHandler_Captures5xxOnly(t *testing.T) {
	sink := newSinkClient(t)

	inner := func(c *fiber.Ctx, err error) error {
		status, body := xerrs.HTTP(err)
		return c.Status(status).JSON(body)
	}
	app := fiber.New(fiber.Config{
		ErrorHandler: sentrykit.WrapErrorHandler(inner),
	})
	app.Use(sentrykit.FiberMiddleware())
	app.Get("/internal", func(c *fiber.Ctx) error {
		return xerrs.Internal("test_internal", "boom")
	})
	app.Get("/missing", func(c *fiber.Ctx) error {
		return xerrs.NotFound("test_missing", "nope")
	})

	if _, err := app.Test(httptest.NewRequest("GET", "/internal", nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Test(httptest.NewRequest("GET", "/missing", nil)); err != nil {
		t.Fatal(err)
	}

	events := sink.snapshot()
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1 (only the 500 should capture)", len(events))
	}
}

func TestHubFromContext_FallsBackToGlobalWithoutMiddleware(t *testing.T) {
	_ = newSinkClient(t)

	app := fiber.New()
	var hub *sentry.Hub
	app.Get("/", func(c *fiber.Ctx) error {
		hub = sentrykit.HubFromContext(c)
		return c.SendString("ok")
	})
	if _, err := app.Test(httptest.NewRequest("GET", "/", nil)); err != nil {
		t.Fatal(err)
	}
	if hub == nil {
		t.Fatal("HubFromContext returned nil — expected global fallback")
	}
	if hub != sentry.CurrentHub() {
		t.Error("expected the process-global hub when FiberMiddleware isn't installed")
	}
}