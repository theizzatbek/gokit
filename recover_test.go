package fibermap

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestRecover_TurnsPanicInto500(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	app := fiber.New()
	app.Use(Recover(logger))
	app.Get("/boom", func(c *fiber.Ctx) error {
		panic("kaboom-test-panic")
	})

	resp, err := app.Test(httptest.NewRequest("GET", "/boom", nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != fiber.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
}

func TestRecover_LogsPanicWithContext(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	app := fiber.New()
	app.Use(RequestID()) // populate request_id before panic
	app.Use(Recover(logger))
	app.Get("/boom", func(c *fiber.Ctx) error {
		panic("kaboom-with-rid")
	})

	if _, err := app.Test(httptest.NewRequest("GET", "/boom", nil)); err != nil {
		t.Fatal(err)
	}

	out := buf.String()
	if !strings.Contains(out, "kaboom-with-rid") {
		t.Errorf("log missing panic payload: %s", out)
	}

	// JSON-parse each line to assert structured fields.
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		if rec["msg"] != "panic recovered" {
			continue
		}
		if rec["method"] != "GET" {
			t.Errorf("method = %v, want GET", rec["method"])
		}
		if rec["path"] != "/boom" {
			t.Errorf("path = %v, want /boom", rec["path"])
		}
		if rid, _ := rec["request_id"].(string); len(rid) != 16 {
			t.Errorf("request_id should be 16 hex chars, got %q", rid)
		}
		if stack, _ := rec["stack"].(string); !strings.Contains(stack, "runtime/debug.Stack") {
			t.Errorf("stack should contain debug.Stack frame, got: %s", stack)
		}
		return
	}
	t.Fatalf("no 'panic recovered' log line found in output: %s", out)
}

func TestRecover_NilLoggerUsesDefault(t *testing.T) {
	app := fiber.New()
	app.Use(Recover(nil))
	app.Get("/boom", func(c *fiber.Ctx) error { panic("x") })

	resp, err := app.Test(httptest.NewRequest("GET", "/boom", nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != fiber.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
}

func TestRun_WithRecover_CatchesPanic(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	e := newTestEngine()
	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) { return engCtx{}, nil })
	e.RegisterHandler("ping.handle", func(c *Context[engCtx]) error {
		panic("handler-panic")
	})

	addr, runErr, stop := runAndWait(t, e,
		WithRoutesPath("testdata/basic.yaml"),
		WithRecover(logger),
	)
	defer stop()

	resp, err := newHTTPClient().Get("http://" + addr + "/v1/ping")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != fiber.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
	if !strings.Contains(buf.String(), "handler-panic") {
		t.Errorf("logger didn't capture the panic: %s", buf.String())
	}
	stop()
	<-runErr
}
