package fibermap

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
)

// ── A. CORS ────────────────────────────────────────────────────────

func TestCORS_DefaultsAllowAny(t *testing.T) {
	t.Parallel()
	app := fiber.New()
	app.Use(CORS())
	app.Get("/x", func(c *fiber.Ctx) error { return c.SendStatus(200) })

	req := httptest.NewRequest("OPTIONS", "/x", nil)
	req.Header.Set("Origin", "https://example.com")
	req.Header.Set("Access-Control-Request-Method", "GET")
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got == "" {
		t.Errorf("missing CORS allow-origin header; got headers = %v", resp.Header)
	}
}

func TestCORS_CustomAllowOrigins(t *testing.T) {
	t.Parallel()
	app := fiber.New()
	app.Use(CORS(CORSConfig{AllowOrigins: "https://allowed.com"}))
	app.Get("/x", func(c *fiber.Ctx) error { return c.SendStatus(200) })

	req := httptest.NewRequest("OPTIONS", "/x", nil)
	req.Header.Set("Origin", "https://allowed.com")
	req.Header.Set("Access-Control-Request-Method", "GET")
	resp, _ := app.Test(req, -1)
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "https://allowed.com" {
		t.Errorf("allow-origin = %q, want https://allowed.com", got)
	}
}

// ── B. RateLimit (in-process IP) ───────────────────────────────────

func TestRateLimitByIP_BlocksAboveBudget(t *testing.T) {
	t.Parallel()
	app := fiber.New()
	app.Use(rateLimitByIP(2 /* rps */, 2 /* burst */, []string{"/healthz"}))
	app.Get("/x", func(c *fiber.Ctx) error { return c.SendStatus(200) })

	// Burst=2 means first two pass; third hit immediately is rejected.
	var oks, blocks atomic.Int32
	for i := 0; i < 5; i++ {
		resp, _ := app.Test(httptest.NewRequest("GET", "/x", nil), -1)
		switch resp.StatusCode {
		case 200:
			oks.Add(1)
		case http.StatusTooManyRequests:
			blocks.Add(1)
		}
	}
	if oks.Load() < 2 {
		t.Errorf("oks = %d, want >= 2", oks.Load())
	}
	if blocks.Load() < 1 {
		t.Errorf("blocks = %d, want >= 1 (rate limit must fire)", blocks.Load())
	}
}

func TestRateLimitByIP_SkipsConfiguredPaths(t *testing.T) {
	t.Parallel()
	app := fiber.New()
	app.Use(rateLimitByIP(1, 1, []string{"/healthz"}))
	app.Get("/healthz", func(c *fiber.Ctx) error { return c.SendString("ok") })

	// 5 hits to /healthz — none should be blocked.
	for i := 0; i < 5; i++ {
		resp, _ := app.Test(httptest.NewRequest("GET", "/healthz", nil), -1)
		if resp.StatusCode != 200 {
			t.Errorf("hit %d: status = %d, want 200 (skip)", i, resp.StatusCode)
		}
	}
}

// ── C. BodyLimit ───────────────────────────────────────────────────

func TestBodyLimit_RejectsOversized(t *testing.T) {
	t.Parallel()
	// No fiber.Config.BodyLimit — let the kit middleware be the
	// gate.
	app := fiber.New()
	app.Use(BodyLimit(50))
	app.Post("/x", func(c *fiber.Ctx) error { return c.SendString("ok") })

	big := bytes.Repeat([]byte("a"), 200)
	req := httptest.NewRequest("POST", "/x", bytes.NewReader(big))
	req.ContentLength = int64(len(big))
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", resp.StatusCode)
	}
}

func TestBodyLimit_ZeroDisables(t *testing.T) {
	t.Parallel()
	app := fiber.New()
	app.Use(BodyLimit(0)) // zero = no-op
	app.Post("/x", func(c *fiber.Ctx) error { return c.SendString("ok") })

	body := bytes.Repeat([]byte("a"), 1000)
	req := httptest.NewRequest("POST", "/x", bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	resp, _ := app.Test(req, -1)
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200 (zero limit = no check)", resp.StatusCode)
	}
}

// ── D. Compression ─────────────────────────────────────────────────

func TestCompression_GzipResponse(t *testing.T) {
	t.Parallel()
	app := fiber.New()
	app.Use(Compression(CompressionBestSpeed))
	// Use a body large enough that compression actually triggers.
	body := bytes.Repeat([]byte("compressible-content "), 200)
	app.Get("/x", func(c *fiber.Ctx) error {
		c.Set("Content-Type", "text/plain")
		return c.Send(body)
	})

	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	resp, _ := app.Test(req, -1)
	if ce := resp.Header.Get("Content-Encoding"); ce != "gzip" {
		t.Errorf("Content-Encoding = %q, want gzip", ce)
	}
}

// ── G. Slow request threshold ──────────────────────────────────────

func TestRequestLogger_SlowThresholdPromotesToWarn(t *testing.T) {
	t.Parallel()
	var buf threadSafeBuffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	app := fiber.New()
	app.Use(RequestLoggerWithOptions(logger,
		WithReqLogSlowThreshold(20*time.Millisecond),
	))
	app.Get("/slow", func(c *fiber.Ctx) error {
		time.Sleep(50 * time.Millisecond)
		return c.SendString("ok")
	})
	app.Get("/fast", func(c *fiber.Ctx) error { return c.SendString("ok") })

	_, _ = app.Test(httptest.NewRequest("GET", "/slow", nil), -1)
	_, _ = app.Test(httptest.NewRequest("GET", "/fast", nil), -1)

	logged := buf.String()
	// /slow should be WARN, /fast should be DEBUG.
	if !strings.Contains(logged, `"level":"WARN"`) {
		t.Errorf("expected WARN level for slow request; logs: %s", logged)
	}
	if !strings.Contains(logged, `"level":"DEBUG"`) {
		t.Errorf("expected DEBUG level for fast request; logs: %s", logged)
	}
}

// ── H. NotFoundJSON ────────────────────────────────────────────────

func TestNotFoundJSON_Shape(t *testing.T) {
	t.Parallel()
	app := fiber.New()
	app.Use(NotFoundJSON())

	resp, _ := app.Test(httptest.NewRequest("GET", "/missing", nil), -1)
	if resp.StatusCode != 404 {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
	var body map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body["code"] != "not_found" {
		t.Errorf("code = %q, want not_found", body["code"])
	}
	if body["path"] != "/missing" {
		t.Errorf("path = %q, want /missing", body["path"])
	}
}

// ── helpers ─────────────────────────────────────────────────────────

type threadSafeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *threadSafeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}
func (b *threadSafeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

var _ context.Context // ensure context import stays alive on edits

// silence unused
var _ = sync.Mutex{}
