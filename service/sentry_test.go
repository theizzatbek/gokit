package service

import (
	"context"
	"testing"

	"github.com/getsentry/sentry-go"

	"github.com/theizzatbek/gokit/sentrykit"
)

// dropSentry returns a no-op BeforeSend option that drops every
// captured event. Used so service-level Sentry tests don't try to
// ship to a real DSN.
func dropSentry() sentrykit.Option {
	return sentrykit.WithBeforeSend(func(_ *sentry.Event, _ *sentry.EventHint) *sentry.Event { return nil })
}

const sentryTestDSN = "https://public@o0.ingest.sentry.io/0"

func TestSetupSentry_EmptyDSNIsNoop(t *testing.T) {
	s := &Service[struct{}, struct{}]{opts: &options{}}
	if err := s.setupSentry(context.Background()); err != nil {
		t.Fatalf("setupSentry: %v", err)
	}
	if s.sentryShutdown != nil {
		t.Errorf("sentryShutdown should be nil when WithSentry wasn't passed")
	}
	if len(s.opts.fiberMiddleware) != 0 {
		t.Errorf("fiberMiddleware = %d, want 0", len(s.opts.fiberMiddleware))
	}
}

func TestSetupSentry_InstallsFiberMiddleware(t *testing.T) {
	s := &Service[struct{}, struct{}]{
		opts: &options{
			sentryDSN:  sentryTestDSN,
			sentryOpts: []sentrykit.Option{dropSentry()},
		},
	}
	if err := s.setupSentry(context.Background()); err != nil {
		t.Fatalf("setupSentry: %v", err)
	}
	if s.sentryShutdown == nil {
		t.Error("sentryShutdown should be non-nil after Setup")
	}
	if len(s.opts.fiberMiddleware) != 1 {
		t.Errorf("fiberMiddleware = %d, want 1 (sentry appended)", len(s.opts.fiberMiddleware))
	}
	// Tear down so subsequent tests start from a clean global hub.
	_ = s.sentryShutdown(context.Background())
}

func TestWithSentry_StoresConfigOnOptions(t *testing.T) {
	o := &options{}
	WithSentry("dsn-xyz", sentrykit.WithEnvironment("staging"))(o)
	if o.sentryDSN != "dsn-xyz" {
		t.Errorf("sentryDSN = %q, want dsn-xyz", o.sentryDSN)
	}
	if len(o.sentryOpts) != 1 {
		t.Errorf("sentryOpts = %d, want 1", len(o.sentryOpts))
	}
}

func TestRegisterSentryShutdown_NoopWhenNil(t *testing.T) {
	s := &Service[struct{}, struct{}]{opts: &options{}}
	s.registerSentryShutdown() // must not panic
	if len(s.shutdownFns) != 0 {
		t.Errorf("shutdownFns = %d, want 0", len(s.shutdownFns))
	}
}

func TestRegisterSentryShutdown_AddsCallback(t *testing.T) {
	called := false
	s := &Service[struct{}, struct{}]{
		opts: &options{},
		sentryShutdown: func(ctx context.Context) error {
			called = true
			return nil
		},
	}
	s.registerSentryShutdown()
	if len(s.shutdownFns) != 1 {
		t.Fatalf("shutdownFns = %d, want 1", len(s.shutdownFns))
	}
	s.Close()
	if !called {
		t.Errorf("sentryShutdown was not invoked during Close")
	}
}

func TestRegisterSentryShutdown_LIFO_AfterOtel(t *testing.T) {
	// OnShutdown is LIFO. We want sentry to flush BEFORE otel — so
	// we register otel first, then sentry. Calling Close should
	// invoke sentry first, then otel.
	var order []string
	s := &Service[struct{}, struct{}]{
		opts: &options{},
		otelShutdown: func(ctx context.Context) error {
			order = append(order, "otel")
			return nil
		},
		sentryShutdown: func(ctx context.Context) error {
			order = append(order, "sentry")
			return nil
		},
	}
	s.registerOtelShutdown()
	s.registerSentryShutdown()
	s.Close()
	if len(order) != 2 || order[0] != "sentry" || order[1] != "otel" {
		t.Errorf("shutdown order = %v, want [sentry otel]", order)
	}
}