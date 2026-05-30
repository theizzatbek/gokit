package sentrykit_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"

	"github.com/theizzatbek/gokit/sentrykit"
)

// errorEventsByMessage indexes captured Sentry events by Message,
// returning the slice of events that match the given message.
func eventsForMessage(events []*sentry.Event, msg string) []*sentry.Event {
	var out []*sentry.Event
	for _, e := range events {
		if e.Message == msg {
			out = append(out, e)
			continue
		}
		// Exception events carry the message via the first Exception
		// entry's Value rather than Event.Message.
		for _, ex := range e.Exception {
			if ex.Value == msg {
				out = append(out, e)
				break
			}
		}
	}
	return out
}

func TestSlogHandler_CaptureLevel_OffByDefault(t *testing.T) {
	sink := newBreadcrumbSink(t)
	logger, _ := newSlogger(t) // no WithCaptureLevel

	hub := sentry.CurrentHub()
	hub.PushScope()
	defer hub.PopScope()
	ctx := sentry.SetHubOnContext(context.Background(), hub)

	logger.ErrorContext(ctx, "should not capture")

	if got := sink.snapshot(); len(got) != 0 {
		t.Errorf("got %d events, want 0 (capture off by default)", len(got))
	}
}

func TestSlogHandler_CaptureLevel_PromotesErrorToEvent(t *testing.T) {
	sink := newBreadcrumbSink(t)
	logger, _ := newSlogger(t,
		sentrykit.WithCaptureLevel(slog.LevelError),
		sentrykit.WithCaptureDedupeWindow(0), // disable dedupe
	)

	hub := sentry.CurrentHub()
	hub.PushScope()
	defer hub.PopScope()
	ctx := sentry.SetHubOnContext(context.Background(), hub)

	logger.ErrorContext(ctx, "kaboom", "k", "v")

	events := sink.snapshot()
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	if events[0].Message != "kaboom" {
		t.Errorf("Message = %q, want kaboom", events[0].Message)
	}
	if events[0].Tags["category"] != "kaboom" {
		t.Errorf("category tag = %q, want kaboom (fallback)", events[0].Tags["category"])
	}
	if lvl := events[0].Level; lvl != sentry.LevelError {
		t.Errorf("Level = %v, want error", lvl)
	}
}

func TestSlogHandler_CaptureLevel_BelowThresholdDoesNotCapture(t *testing.T) {
	sink := newBreadcrumbSink(t)
	logger, _ := newSlogger(t,
		sentrykit.WithCaptureLevel(slog.LevelError),
		sentrykit.WithCaptureDedupeWindow(0),
	)

	hub := sentry.CurrentHub()
	hub.PushScope()
	defer hub.PopScope()
	ctx := sentry.SetHubOnContext(context.Background(), hub)

	logger.WarnContext(ctx, "warning")
	logger.InfoContext(ctx, "info")

	if got := sink.snapshot(); len(got) != 0 {
		t.Errorf("got %d events, want 0 (Warn/Info below threshold)", len(got))
	}
}

func TestSlogHandler_CaptureLevel_ErrorAttrProducesException(t *testing.T) {
	sink := newBreadcrumbSink(t)
	logger, _ := newSlogger(t,
		sentrykit.WithCaptureLevel(slog.LevelError),
		sentrykit.WithCaptureDedupeWindow(0),
	)

	hub := sentry.CurrentHub()
	hub.PushScope()
	defer hub.PopScope()
	ctx := sentry.SetHubOnContext(context.Background(), hub)

	boom := errors.New("real-failure")
	logger.ErrorContext(ctx, "db query failed", "err", boom)

	events := sink.snapshot()
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	if len(events[0].Exception) == 0 {
		t.Fatalf("expected Exception event, got Message=%q Exception=%v",
			events[0].Message, events[0].Exception)
	}
	if got := events[0].Exception[0].Value; got != "real-failure" {
		t.Errorf("Exception.Value = %q, want real-failure", got)
	}
}

func TestSlogHandler_CaptureLevel_NoErrorAttrFallsToMessage(t *testing.T) {
	sink := newBreadcrumbSink(t)
	logger, _ := newSlogger(t,
		sentrykit.WithCaptureLevel(slog.LevelError),
		sentrykit.WithCaptureDedupeWindow(0),
	)

	hub := sentry.CurrentHub()
	hub.PushScope()
	defer hub.PopScope()
	ctx := sentry.SetHubOnContext(context.Background(), hub)

	logger.ErrorContext(ctx, "synthetic error", "ip", "1.2.3.4")

	events := sink.snapshot()
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	if events[0].Message != "synthetic error" {
		t.Errorf("Message = %q, want 'synthetic error'", events[0].Message)
	}
	if len(events[0].Exception) > 0 {
		t.Errorf("expected Message event, got Exception=%v", events[0].Exception)
	}
}

func TestSlogHandler_CaptureLevel_CustomErrorAttrKeys(t *testing.T) {
	sink := newBreadcrumbSink(t)
	logger, _ := newSlogger(t,
		sentrykit.WithCaptureLevel(slog.LevelError),
		sentrykit.WithCaptureErrorAttrKeys("inner"),
		sentrykit.WithCaptureDedupeWindow(0),
	)

	hub := sentry.CurrentHub()
	hub.PushScope()
	defer hub.PopScope()
	ctx := sentry.SetHubOnContext(context.Background(), hub)

	// Default keys would match `err`; ensure we ignore that and look
	// only at `inner`.
	defaultMatchedKey := errors.New("default-key")
	customMatchedKey := errors.New("custom-key")
	logger.ErrorContext(ctx, "broken", "err", defaultMatchedKey, "inner", customMatchedKey)

	events := sink.snapshot()
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	if len(events[0].Exception) == 0 {
		t.Fatal("expected Exception event")
	}
	if got := events[0].Exception[0].Value; got != "custom-key" {
		t.Errorf("Exception.Value = %q, want custom-key (default 'err' should be ignored)", got)
	}
}

func TestSlogHandler_CaptureLevel_Dedupe(t *testing.T) {
	sink := newBreadcrumbSink(t)
	logger, _ := newSlogger(t,
		sentrykit.WithCaptureLevel(slog.LevelError),
		sentrykit.WithCaptureDedupeWindow(60*time.Second),
	)

	hub := sentry.CurrentHub()
	hub.PushScope()
	defer hub.PopScope()
	ctx := sentry.SetHubOnContext(context.Background(), hub)

	for range 5 {
		logger.ErrorContext(ctx, "repeated", "category", "db")
	}

	events := sink.snapshot()
	if len(eventsForMessage(events, "repeated")) != 1 {
		t.Errorf("got %d 'repeated' events, want 1 (4 dedupe-suppressed)", len(eventsForMessage(events, "repeated")))
	}
}

func TestSlogHandler_CaptureLevel_DedupeDifferentMessagesNotMerged(t *testing.T) {
	sink := newBreadcrumbSink(t)
	logger, _ := newSlogger(t,
		sentrykit.WithCaptureLevel(slog.LevelError),
		sentrykit.WithCaptureDedupeWindow(60*time.Second),
	)

	hub := sentry.CurrentHub()
	hub.PushScope()
	defer hub.PopScope()
	ctx := sentry.SetHubOnContext(context.Background(), hub)

	logger.ErrorContext(ctx, "one", "category", "db")
	logger.ErrorContext(ctx, "two", "category", "db")

	events := sink.snapshot()
	if len(eventsForMessage(events, "one")) != 1 {
		t.Errorf("missing 'one' event")
	}
	if len(eventsForMessage(events, "two")) != 1 {
		t.Errorf("missing 'two' event")
	}
}

func TestSlogHandler_CaptureLevel_BreadcrumbStillAddsOnDedupe(t *testing.T) {
	sink := newBreadcrumbSink(t)
	logger, _ := newSlogger(t,
		sentrykit.WithCaptureLevel(slog.LevelError),
		sentrykit.WithCaptureDedupeWindow(60*time.Second),
	)

	hub := sentry.CurrentHub()
	hub.PushScope()
	defer hub.PopScope()
	ctx := sentry.SetHubOnContext(context.Background(), hub)

	logger.ErrorContext(ctx, "first", "category", "db") // captures + breadcrumb
	logger.ErrorContext(ctx, "first", "category", "db") // dedupe-suppressed; still breadcrumb
	captureNow(ctx, "later trigger")                    // forces a fresh event so we can inspect breadcrumbs

	events := sink.snapshot()
	trigger := eventsForMessage(events, "later trigger")
	if len(trigger) != 1 {
		t.Fatalf("trigger event count = %d, want 1", len(trigger))
	}
	// Expect TWO 'first' breadcrumbs even though only the first
	// captured as an event.
	var count int
	for _, bc := range trigger[0].Breadcrumbs {
		if bc.Message == "first" {
			count++
		}
	}
	if count != 2 {
		t.Errorf("'first' breadcrumb count = %d, want 2 (both calls breadcrumb regardless of dedupe)", count)
	}
}

func TestSlogHandler_CaptureLevel_PacksAttrsIntoLogContext(t *testing.T) {
	sink := newBreadcrumbSink(t)
	logger, _ := newSlogger(t,
		sentrykit.WithCaptureLevel(slog.LevelError),
		sentrykit.WithCaptureDedupeWindow(0),
	)

	hub := sentry.CurrentHub()
	hub.PushScope()
	defer hub.PopScope()
	ctx := sentry.SetHubOnContext(context.Background(), hub)

	logger.ErrorContext(ctx, "boom", "ip", "1.2.3.4", "attempts", 3)

	events := sink.snapshot()
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	logCtx, ok := events[0].Contexts["log"]
	if !ok {
		t.Fatalf("event missing 'log' context block; Contexts=%+v", events[0].Contexts)
	}
	if logCtx["ip"] != "1.2.3.4" {
		t.Errorf("log.ip = %v, want 1.2.3.4", logCtx["ip"])
	}
	if logCtx["attempts"] != int64(3) {
		t.Errorf("log.attempts = %v, want 3", logCtx["attempts"])
	}
}
