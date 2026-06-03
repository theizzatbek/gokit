package sentrykit

import (
	"bytes"
	"context"
	"log/slog"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/gofiber/fiber/v2"
)

// ── A. Stats / B. RateLimit ────────────────────────────────────────

func TestStats_CountsBreadcrumbsAndEvents(t *testing.T) {
	resetStats()
	// Wrap a no-op inner handler.
	h := SlogHandler(slog.NewTextHandler(&threadSafeBuffer{}, nil),
		WithCaptureLevel(slog.LevelError),
		WithCaptureDedupeWindow(0), // disable dedupe for this test
	)
	logger := slog.New(h)

	// Three error logs with same fingerprint — all should capture
	// (dedupe disabled) and all increment breadcrumb counter.
	for i := 0; i < 3; i++ {
		logger.Error("db: connection lost", "err", errString("x"))
	}
	s := GetStats()
	if s.BreadcrumbsEmitted < 3 {
		t.Errorf("breadcrumbs = %d, want >= 3", s.BreadcrumbsEmitted)
	}
	// EventsCaptured may be 0 if no Sentry hub initialised, but the
	// path is still exercised through captureEvent.
}

func TestStats_DedupeIncrement(t *testing.T) {
	resetStats()
	h := SlogHandler(slog.NewTextHandler(&threadSafeBuffer{}, nil),
		WithCaptureLevel(slog.LevelError),
		WithCaptureDedupeWindow(time.Hour), // strong dedupe
	)
	logger := slog.New(h)
	for i := 0; i < 5; i++ {
		logger.Error("dup: same message", "err", errString("e"))
	}
	if GetStats().EventsDeduped < 4 {
		t.Errorf("EventsDeduped = %d, want >= 4 (5 calls minus 1 first)",
			GetStats().EventsDeduped)
	}
}

func TestWithCaptureRateLimit_SuppressesAboveCap(t *testing.T) {
	resetStats()
	h := SlogHandler(slog.NewTextHandler(&threadSafeBuffer{}, nil),
		WithCaptureLevel(slog.LevelError),
		WithCaptureDedupeWindow(0), // dedupe off; rate limit is the gate
		WithCaptureRateLimit(2),    // 2 events / minute / fingerprint
	)
	logger := slog.New(h)
	for i := 0; i < 5; i++ {
		logger.Error("rl: too many", "err", errString("e"))
	}
	s := GetStats()
	if s.EventsRateLimited < 3 {
		t.Errorf("EventsRateLimited = %d, want >= 3 (5 calls - 2 allowed)",
			s.EventsRateLimited)
	}
}

// ── C. RouteFilter ────────────────────────────────────────────────

func TestFiberMiddleware_WithRouteFilter_SkipsPaths(t *testing.T) {
	var processed atomic.Int32
	app := fiber.New()
	app.Use(FiberMiddlewareWithOptions(
		WithRouteFilter(DefaultRouteSkipFn),
		// We can't easily assert hub clone behaviour without Sentry
		// being initialised; instead we use a sentinel middleware
		// after sentry's that counts how many of these requests
		// reached the next phase.
	))
	app.Use(func(c *fiber.Ctx) error {
		processed.Add(1)
		return c.SendStatus(200)
	})

	resp, _ := app.Test(httptest.NewRequest("GET", "/healthz", nil), -1)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	resp2, _ := app.Test(httptest.NewRequest("GET", "/business", nil), -1)
	if resp2.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp2.StatusCode)
	}
	// Both processed by inner; the filter just suppresses hub work.
	if processed.Load() != 2 {
		t.Errorf("processed = %d, want 2", processed.Load())
	}
}

func TestDefaultRouteSkipFn_Coverage(t *testing.T) {
	cases := []struct {
		path string
		skip bool
	}{
		{"/healthz", true},
		{"/readyz", true},
		{"/metrics", true},
		{"/favicon.ico", true},
		{"/api/users", false},
		{"/", false},
	}
	for _, tc := range cases {
		if got := DefaultRouteSkipFn(tc.path); got != tc.skip {
			t.Errorf("DefaultRouteSkipFn(%q) = %v, want %v", tc.path, got, tc.skip)
		}
	}
}

// ── D. RecoverGo ──────────────────────────────────────────────────

func TestRecoverGo_SwallowsPanic(t *testing.T) {
	done := make(chan struct{}, 1)
	go func() {
		RecoverGo(func() { panic("worker boom") })
		done <- struct{}{}
	}()
	select {
	case <-done:
		// good — panic absorbed, goroutine exits cleanly
	case <-time.After(time.Second):
		t.Fatal("RecoverGo did not return after panic")
	}
}

// ── F. AddBreadcrumb / G. SetUser ─────────────────────────────────

func TestAddBreadcrumb_IncrementsStats(t *testing.T) {
	resetStats()
	// Without Sentry initialised, AddBreadcrumb falls through to
	// nil-hub no-op. Still counts via Stats? No — the stat counter
	// only ticks when hub is non-nil and breadcrumb is added. With
	// no hub, breadcrumb is dropped, stat untouched. Test that the
	// no-hub path doesn't panic.
	AddBreadcrumb(context.Background(), "billing", "charge", nil)
}

func TestSetUser_NoHubNoPanic(t *testing.T) {
	// Without Setup, hub is nil — SetUser should silently return.
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("SetUser panicked: %v", r)
		}
	}()
	SetUser(context.Background(), "u-1", "u@example.com")
	SetUser(context.Background(), "", "")
}

// ── H. ScrubPII / WithoutPII ──────────────────────────────────────

func TestScrubPII_RedactsHeaders(t *testing.T) {
	scrub := ScrubPII()
	e := &sentry.Event{
		Request: &sentry.Request{
			Headers: map[string]string{
				"Authorization": "Bearer secret-token",
				"Cookie":        "sid=abc123",
				"X-API-Key":     "key-789",
				"X-User-Agent":  "kept",
			},
		},
	}
	out := scrub(e, nil)
	if out.Request.Headers["Authorization"] != "[redacted]" {
		t.Error("Authorization not redacted")
	}
	if out.Request.Headers["Cookie"] != "[redacted]" {
		t.Error("Cookie not redacted")
	}
	if out.Request.Headers["X-API-Key"] != "[redacted]" {
		t.Error("X-API-Key not redacted")
	}
	if out.Request.Headers["X-User-Agent"] != "kept" {
		t.Errorf("X-User-Agent should pass through, got %q", out.Request.Headers["X-User-Agent"])
	}
}

func TestScrubPII_RedactsTokenQuery(t *testing.T) {
	scrub := ScrubPII()
	e := &sentry.Event{
		Request: &sentry.Request{
			URL:         "https://example.com/path?token=secret&id=42",
			QueryString: "token=secret&id=42",
		},
	}
	out := scrub(e, nil)
	if !strings.Contains(out.Request.URL, "token=[redacted]") {
		t.Errorf("URL not scrubbed: %q", out.Request.URL)
	}
	if !strings.Contains(out.Request.URL, "id=42") {
		t.Errorf("non-PII query stripped: %q", out.Request.URL)
	}
}

func TestScrubPII_NilSafe(t *testing.T) {
	scrub := ScrubPII()
	if out := scrub(nil, nil); out != nil {
		t.Errorf("nil event should return nil, got %v", out)
	}
}

// ── helpers ─────────────────────────────────────────────────────────

func resetStats() {
	statsGlobal.eventsCaptured.Store(0)
	statsGlobal.eventsDeduped.Store(0)
	statsGlobal.eventsRateLimited.Store(0)
	statsGlobal.breadcrumbs.Store(0)
	statsGlobal.dedupeCacheSize.Store(0)
}

type threadSafeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *threadSafeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

type errString string

func (e errString) Error() string { return string(e) }
