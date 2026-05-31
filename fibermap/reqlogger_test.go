package fibermap

import (
	"bytes"
	"context"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
)

// captureLogger writes JSON to buf so tests can assert attrs.
func captureLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func TestLoggerInjector_BindsMethodPathRequestID(t *testing.T) {
	var buf bytes.Buffer
	base := captureLogger(&buf)

	app := fiber.New()
	app.Use(RequestID())
	app.Use(LoggerInjector(base))
	app.Get("/items/:id", func(c *fiber.Ctx) error {
		LoggerFrom(c).Info("read item", "id", c.Params("id"))
		return c.SendString("ok")
	})

	resp, err := app.Test(httptest.NewRequest("GET", "/items/abc", nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	out := buf.String()
	if !strings.Contains(out, `"method":"GET"`) {
		t.Errorf("missing method attr: %s", out)
	}
	if !strings.Contains(out, `"path":"/items/abc"`) {
		t.Errorf("missing path attr: %s", out)
	}
	if !strings.Contains(out, `"request_id"`) {
		t.Errorf("missing request_id attr: %s", out)
	}
	if !strings.Contains(out, `"id":"abc"`) {
		t.Errorf("missing handler attr: %s", out)
	}
}

func TestLoggerInjector_NoBase_DefaultsToSlog(t *testing.T) {
	app := fiber.New()
	app.Use(LoggerInjector(nil))
	app.Get("/", func(c *fiber.Ctx) error {
		// Must not panic.
		_ = LoggerFrom(c)
		return c.SendString("ok")
	})
	resp, err := app.Test(httptest.NewRequest("GET", "/", nil))
	if err != nil || resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, err = %v", resp.StatusCode, err)
	}
}

func TestLoggerFrom_NoMiddleware_FallsBackToDefault(t *testing.T) {
	app := fiber.New()
	app.Get("/", func(c *fiber.Ctx) error {
		l := LoggerFrom(c)
		if l == nil {
			t.Error("LoggerFrom returned nil; should fall back to default")
		}
		return c.SendString("ok")
	})
	if _, err := app.Test(httptest.NewRequest("GET", "/", nil)); err != nil {
		t.Fatal(err)
	}
}

func TestLoggerFrom_PicksUpAuthSubject(t *testing.T) {
	var buf bytes.Buffer
	base := captureLogger(&buf)

	app := fiber.New()
	app.Use(LoggerInjector(base))
	app.Use(func(c *fiber.Ctx) error {
		// Stand-in for the kit's auth Bearer middleware populating
		// the subject under the shared Locals slot.
		c.Locals(LocalsAuthSubject, "user-42")
		return c.Next()
	})
	app.Get("/", func(c *fiber.Ctx) error {
		LoggerFrom(c).Info("handler ran")
		return c.SendString("ok")
	})

	if _, err := app.Test(httptest.NewRequest("GET", "/", nil)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"user_id":"user-42"`) {
		t.Errorf("expected user_id attr; got: %s", buf.String())
	}
}

func TestLoggerFrom_NilCtx_ReturnsDefault(t *testing.T) {
	l := LoggerFrom(nil)
	if l == nil {
		t.Error("LoggerFrom(nil) should not return nil")
	}
}

func TestLoggerFrom_RouteName_AppendedWhenSet(t *testing.T) {
	var buf bytes.Buffer
	base := captureLogger(&buf)

	app := fiber.New()
	app.Use(LoggerInjector(base))
	app.Get("/", func(c *fiber.Ctx) error {
		LoggerFrom(c).Info("ran")
		return c.SendString("ok")
	}).Name("home")
	if _, err := app.Test(httptest.NewRequest("GET", "/", nil)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"route":"home"`) {
		t.Errorf("expected route attr: %s", buf.String())
	}
}

func TestLoggerFrom_ConsistentAcrossCalls(t *testing.T) {
	// Same ctx, two LoggerFrom calls should return loggers that emit
	// the same base attrs (so devs don't worry about caching).
	var buf bytes.Buffer
	base := captureLogger(&buf)
	_ = context.Background()

	app := fiber.New()
	app.Use(LoggerInjector(base))
	app.Get("/", func(c *fiber.Ctx) error {
		LoggerFrom(c).Info("a")
		LoggerFrom(c).Info("b")
		return c.SendString("ok")
	})
	if _, err := app.Test(httptest.NewRequest("GET", "/", nil)); err != nil {
		t.Fatal(err)
	}
	if strings.Count(buf.String(), `"method":"GET"`) != 2 {
		t.Errorf("both calls should carry method; got: %s", buf.String())
	}
}
