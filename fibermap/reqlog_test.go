package fibermap

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/theizzatbek/gokit/errs"
)

func collectLogLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("bad log line %q: %v", line, err)
		}
		out = append(out, rec)
	}
	return out
}

func TestRequestLogger_LogsBasicFields(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	app := fiber.New()
	app.Use(RequestID(), RequestLogger(logger))
	app.Get("/ping", func(c *fiber.Ctx) error { return c.SendString("pong") })

	resp, err := app.Test(httptest.NewRequest("GET", "/ping", nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	lines := collectLogLines(t, &buf)
	if len(lines) != 1 {
		t.Fatalf("got %d log lines, want 1: %v", len(lines), lines)
	}
	r := lines[0]
	if r["msg"] != "http request" {
		t.Errorf("msg = %v", r["msg"])
	}
	if r["method"] != "GET" {
		t.Errorf("method = %v", r["method"])
	}
	if r["path"] != "/ping" {
		t.Errorf("path = %v", r["path"])
	}
	if int(r["status"].(float64)) != 200 {
		t.Errorf("status = %v", r["status"])
	}
	if int(r["bytes"].(float64)) != 4 { // "pong"
		t.Errorf("bytes = %v", r["bytes"])
	}
	if rid, _ := r["request_id"].(string); len(rid) != 16 {
		t.Errorf("request_id = %q, want 16 hex chars", rid)
	}
}

func TestRequestLogger_ErrorLevelOn500(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	app := fiber.New()
	app.Use(RequestLogger(logger))
	app.Get("/boom", func(c *fiber.Ctx) error {
		return c.Status(500).SendString("nope")
	})

	if _, err := app.Test(httptest.NewRequest("GET", "/boom", nil)); err != nil {
		t.Fatal(err)
	}
	lines := collectLogLines(t, &buf)
	if len(lines) != 1 {
		t.Fatalf("got %d lines", len(lines))
	}
	if lines[0]["level"] != "ERROR" {
		t.Errorf("level = %v, want ERROR (status was 500)", lines[0]["level"])
	}
}

func TestRequestLogger_SkipPath(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	app := fiber.New()
	app.Use(RequestLogger(logger, "/healthz", "/metrics"))
	app.Get("/healthz", func(c *fiber.Ctx) error { return c.SendString("ok") })
	app.Get("/api", func(c *fiber.Ctx) error { return c.SendString("real") })

	if _, err := app.Test(httptest.NewRequest("GET", "/healthz", nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Test(httptest.NewRequest("GET", "/api", nil)); err != nil {
		t.Fatal(err)
	}

	lines := collectLogLines(t, &buf)
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1 (healthz must be skipped)", len(lines))
	}
	if lines[0]["path"] != "/api" {
		t.Errorf("path = %v", lines[0]["path"])
	}
}

// Regression: gokit middlewares reject requests by returning *errs.Error
// while the HTTP status is written later by ErrorHandler mounted in
// fiber.Config — outside the Use chain. RequestLogger must classify by
// the returned error, not the not-yet-written response status (which is
// still the default 200 at that point).
func TestRequestLogger_StatusFromMiddlewareError(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		wantCode  int
		wantLevel string
	}{
		{"unauthorized", errs.Unauthorized("missing_token", "no bearer token"), 401, "INFO"},
		{"forbidden", errs.Permission("require_role", "admin role required"), 403, "INFO"},
		{"internal", errs.Internal("boom", "backend down"), 500, "ERROR"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&buf, nil))

			app := fiber.New(fiber.Config{ErrorHandler: ErrorHandler(slog.New(slog.NewJSONHandler(io.Discard, nil)))})
			app.Use(RequestLogger(logger))
			app.Use(func(c *fiber.Ctx) error { return tc.err })
			app.Get("/secure", func(c *fiber.Ctx) error { return c.SendString("never") })

			resp, err := app.Test(httptest.NewRequest("GET", "/secure", nil))
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != tc.wantCode {
				t.Fatalf("client saw status = %d, want %d", resp.StatusCode, tc.wantCode)
			}

			lines := collectLogLines(t, &buf)
			if len(lines) != 1 {
				t.Fatalf("got %d log lines, want 1: %v", len(lines), lines)
			}
			r := lines[0]
			if int(r["status"].(float64)) != tc.wantCode {
				t.Errorf("logged status = %v, want %d", r["status"], tc.wantCode)
			}
			if r["level"] != tc.wantLevel {
				t.Errorf("level = %v, want %s", r["level"], tc.wantLevel)
			}
		})
	}
}

func TestRequestLogger_NilLoggerUsesDefault(t *testing.T) {
	// Just verify it doesn't panic and the middleware works.
	app := fiber.New()
	app.Use(RequestLogger(nil))
	app.Get("/", func(c *fiber.Ctx) error { return c.SendString("ok") })

	resp, err := app.Test(httptest.NewRequest("GET", "/", nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("status = %d", resp.StatusCode)
	}
}
