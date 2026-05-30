package sentrykit_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/getsentry/sentry-go"
	"github.com/gofiber/fiber/v2"

	"github.com/theizzatbek/gokit/fibermap"
	"github.com/theizzatbek/gokit/sentrykit"
)

// breadcrumbCaptureSink installs a Sentry client whose BeforeSend
// captures the event (including its breadcrumbs) into a slice and
// drops the event before network send. The sink is used to assert
// what the SlogHandler attached to the request hub.
type breadcrumbCaptureSink struct {
	mu     sync.Mutex
	events []*sentry.Event
}

func (s *breadcrumbCaptureSink) snapshot() []*sentry.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*sentry.Event, len(s.events))
	copy(out, s.events)
	return out
}

func newBreadcrumbSink(t *testing.T) *breadcrumbCaptureSink {
	t.Helper()
	sink := &breadcrumbCaptureSink{}
	shutdown, err := sentrykit.Setup(context.Background(), testDSN,
		sentrykit.WithBeforeSend(func(e *sentry.Event, _ *sentry.EventHint) *sentry.Event {
			sink.mu.Lock()
			sink.events = append(sink.events, e)
			sink.mu.Unlock()
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	t.Cleanup(func() { _ = shutdown(context.Background()) })
	return sink
}

// newSlogger wires a slog.Logger whose handler routes through
// SlogHandler(jsonHandler(&buf)). buf is returned so the test can
// also assert inner-handler delivery.
func newSlogger(t *testing.T, opts ...sentrykit.HandlerOption) (*slog.Logger, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	inner := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	return slog.New(sentrykit.SlogHandler(inner, opts...)), &buf
}

// captureForBreadcrumbs takes the hub out of ctx (or global) and
// emits a CaptureMessage so the BeforeSend sink can inspect the
// breadcrumb slice associated with the moment of capture.
func captureNow(ctx context.Context, msg string) {
	hub := sentry.GetHubFromContext(ctx)
	if hub == nil {
		hub = sentry.CurrentHub()
	}
	hub.CaptureMessage(msg)
}

func TestSlogHandler_DelegatesToInner(t *testing.T) {
	_ = newBreadcrumbSink(t)
	logger, buf := newSlogger(t)
	logger.Info("hello", "k", "v")
	if !strings.Contains(buf.String(), "hello") || !strings.Contains(buf.String(), `"k":"v"`) {
		t.Errorf("inner handler did not receive the record; buf=%q", buf.String())
	}
}

func TestSlogHandler_AddsBreadcrumbAtInfoWarnError(t *testing.T) {
	sink := newBreadcrumbSink(t)
	logger, _ := newSlogger(t)

	hub := sentry.CurrentHub()
	hub.PushScope()
	defer hub.PopScope()

	ctx := sentry.SetHubOnContext(context.Background(), hub)
	logger.InfoContext(ctx, "info one")
	logger.WarnContext(ctx, "warn one", "k", "v")
	logger.ErrorContext(ctx, "error one")
	captureNow(ctx, "ship it")

	events := sink.snapshot()
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	got := levelsByMessage(events[0])
	if got["info one"] != sentry.LevelInfo {
		t.Errorf("info one level = %v, want info", got["info one"])
	}
	if got["warn one"] != sentry.LevelWarning {
		t.Errorf("warn one level = %v, want warning", got["warn one"])
	}
	if got["error one"] != sentry.LevelError {
		t.Errorf("error one level = %v, want error", got["error one"])
	}
}

func TestSlogHandler_SkipsDebugByDefault(t *testing.T) {
	sink := newBreadcrumbSink(t)
	logger, buf := newSlogger(t)

	hub := sentry.CurrentHub()
	hub.PushScope()
	defer hub.PopScope()
	ctx := sentry.SetHubOnContext(context.Background(), hub)

	logger.DebugContext(ctx, "noisy debug")
	captureNow(ctx, "ship it")

	if !strings.Contains(buf.String(), "noisy debug") {
		t.Errorf("inner handler should still see debug records; buf=%q", buf.String())
	}
	events := sink.snapshot()
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	if _, ok := levelsByMessage(events[0])["noisy debug"]; ok {
		t.Errorf("debug breadcrumb should be skipped by default")
	}
}

func TestSlogHandler_WithDebugBreadcrumbs_IncludesDebug(t *testing.T) {
	sink := newBreadcrumbSink(t)
	logger, _ := newSlogger(t, sentrykit.WithDebugBreadcrumbs())

	hub := sentry.CurrentHub()
	hub.PushScope()
	defer hub.PopScope()
	ctx := sentry.SetHubOnContext(context.Background(), hub)

	logger.DebugContext(ctx, "noisy debug")
	captureNow(ctx, "ship it")

	got := levelsByMessage(sink.snapshot()[0])
	if got["noisy debug"] != sentry.LevelDebug {
		t.Errorf("WithDebugBreadcrumbs: debug breadcrumb missing or wrong level = %v", got["noisy debug"])
	}
}

func TestSlogHandler_CategoryFromAttr(t *testing.T) {
	sink := newBreadcrumbSink(t)
	logger, _ := newSlogger(t)

	hub := sentry.CurrentHub()
	hub.PushScope()
	defer hub.PopScope()
	ctx := sentry.SetHubOnContext(context.Background(), hub)

	logger.InfoContext(ctx, "some message", "category", "db", "sql", "SELECT 1")
	captureNow(ctx, "ship it")

	bc := findBreadcrumb(sink.snapshot()[0], "some message")
	if bc == nil {
		t.Fatal("breadcrumb not found")
	}
	if bc.Category != "db" {
		t.Errorf("category = %q, want db", bc.Category)
	}
	if _, ok := bc.Data["category"]; ok {
		t.Errorf("category attr should be removed from Data once promoted; got %v", bc.Data)
	}
	if bc.Data["sql"] != "SELECT 1" {
		t.Errorf("sql attr missing/wrong = %v", bc.Data["sql"])
	}
}

func TestSlogHandler_CategoryFromMessageFallback(t *testing.T) {
	sink := newBreadcrumbSink(t)
	logger, _ := newSlogger(t)

	hub := sentry.CurrentHub()
	hub.PushScope()
	defer hub.PopScope()
	ctx := sentry.SetHubOnContext(context.Background(), hub)

	logger.InfoContext(ctx, "httpc retry", "attempt", 2)
	logger.InfoContext(ctx, "db: connect failed")
	captureNow(ctx, "ship it")

	bcs := sink.snapshot()[0].Breadcrumbs
	var sawHTTPC, sawDB bool
	for _, bc := range bcs {
		if bc.Message == "httpc retry" && bc.Category == "httpc" {
			sawHTTPC = true
		}
		if bc.Message == "db: connect failed" && bc.Category == "db" {
			sawDB = true
		}
	}
	if !sawHTTPC {
		t.Errorf("expected httpc category from first word; got bcs=%v", bcs)
	}
	if !sawDB {
		t.Errorf("expected db category from 'db:' prefix; got bcs=%v", bcs)
	}
}

func TestSlogHandler_AttrFilter(t *testing.T) {
	sink := newBreadcrumbSink(t)
	logger, _ := newSlogger(t, sentrykit.WithAttrFilter(func(k string) bool {
		return k != "sql"
	}))

	hub := sentry.CurrentHub()
	hub.PushScope()
	defer hub.PopScope()
	ctx := sentry.SetHubOnContext(context.Background(), hub)

	logger.InfoContext(ctx, "db query", "sql", "SELECT 1", "elapsed_ms", 5)
	captureNow(ctx, "ship it")

	bc := findBreadcrumb(sink.snapshot()[0], "db query")
	if bc == nil {
		t.Fatal("breadcrumb not found")
	}
	if _, ok := bc.Data["sql"]; ok {
		t.Errorf("sql attr should be filtered out")
	}
	if bc.Data["elapsed_ms"] != int64(5) {
		t.Errorf("elapsed_ms = %v, want 5", bc.Data["elapsed_ms"])
	}
}

func TestSlogHandler_ValueLenCap(t *testing.T) {
	sink := newBreadcrumbSink(t)
	logger, _ := newSlogger(t, sentrykit.WithMaxBreadcrumbValueLen(8))

	hub := sentry.CurrentHub()
	hub.PushScope()
	defer hub.PopScope()
	ctx := sentry.SetHubOnContext(context.Background(), hub)

	long := strings.Repeat("x", 64)
	logger.InfoContext(ctx, "noisy", "long", long)
	captureNow(ctx, "ship it")

	bc := findBreadcrumb(sink.snapshot()[0], "noisy")
	if bc == nil {
		t.Fatal("breadcrumb not found")
	}
	got := bc.Data["long"].(string)
	if !strings.HasSuffix(got, "…") || len(got) > 9+3 { // 8 chars + 3-byte ellipsis
		t.Errorf("value not capped: %q (len=%d)", got, len(got))
	}
}

func TestSlogHandler_FallsBackToGlobalHubWithoutContext(t *testing.T) {
	sink := newBreadcrumbSink(t)
	logger, _ := newSlogger(t)

	// Log with the global hub and capture via the same hub — no
	// FiberMiddleware in the picture.
	logger.Info("global breadcrumb")
	sentry.CurrentHub().CaptureMessage("ship it")

	events := sink.snapshot()
	if len(events) == 0 {
		t.Fatal("no events captured")
	}
	if findBreadcrumb(events[len(events)-1], "global breadcrumb") == nil {
		t.Errorf("breadcrumb missing on global-hub capture; bcs=%+v", events[len(events)-1].Breadcrumbs)
	}
}

func TestSlogHandler_UsesRequestHubViaFiberMiddleware(t *testing.T) {
	sink := newBreadcrumbSink(t)
	logger, _ := newSlogger(t)

	app := fiber.New()
	app.Use(fibermap.Recover(nil))
	app.Use(sentrykit.FiberMiddleware())
	bang := errors.New("kaboom")
	app.Get("/boom", func(c *fiber.Ctx) error {
		// Log via *Context variant so the SlogHandler resolves the
		// request hub set by FiberMiddleware on UserContext.
		logger.InfoContext(c.UserContext(), "db query", "category", "db", "rows", 7)
		panic(bang)
	})

	resp, err := app.Test(httptest.NewRequest("GET", "/boom", nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 500 {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}

	events := sink.snapshot()
	if len(events) == 0 {
		t.Fatal("no event captured for the panic")
	}
	bc := findBreadcrumb(events[len(events)-1], "db query")
	if bc == nil {
		t.Fatalf("expected db query breadcrumb on the captured event; bcs=%+v", events[len(events)-1].Breadcrumbs)
	}
	if bc.Category != "db" {
		t.Errorf("category = %q, want db", bc.Category)
	}
	if bc.Data["rows"] != int64(7) {
		t.Errorf("rows attr = %v, want 7", bc.Data["rows"])
	}
}

func TestSlogHandler_WithAttrsAndGroupsFold(t *testing.T) {
	sink := newBreadcrumbSink(t)
	logger, _ := newSlogger(t)

	hub := sentry.CurrentHub()
	hub.PushScope()
	defer hub.PopScope()
	ctx := sentry.SetHubOnContext(context.Background(), hub)

	scoped := logger.With("subsystem", "auth").WithGroup("event")
	scoped.InfoContext(ctx, "login_success", "subject", "u1")
	captureNow(ctx, "ship it")

	bc := findBreadcrumb(sink.snapshot()[0], "login_success")
	if bc == nil {
		t.Fatal("breadcrumb not found")
	}
	if bc.Data["subsystem"] != "auth" {
		t.Errorf("subsystem attr from With() = %v, want auth", bc.Data["subsystem"])
	}
	if bc.Data["event.subject"] != "u1" {
		t.Errorf("grouped attr 'event.subject' = %v, want u1", bc.Data["event.subject"])
	}
}

// findBreadcrumb returns the first breadcrumb in e whose Message
// matches msg, or nil.
func findBreadcrumb(e *sentry.Event, msg string) *sentry.Breadcrumb {
	for i := range e.Breadcrumbs {
		if e.Breadcrumbs[i].Message == msg {
			return e.Breadcrumbs[i]
		}
	}
	return nil
}

// levelsByMessage indexes breadcrumb levels by their Message for
// quick assertion lookups.
func levelsByMessage(e *sentry.Event) map[string]sentry.Level {
	out := map[string]sentry.Level{}
	for _, bc := range e.Breadcrumbs {
		out[bc.Message] = bc.Level
	}
	return out
}
