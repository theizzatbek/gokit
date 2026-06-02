package service

import (
	"log/slog"
	"testing"

	"github.com/theizzatbek/gokit/otelkit"
	"github.com/theizzatbek/gokit/sentrykit"
)

func TestSentryOptions_StructFormPopulatesAllFields(t *testing.T) {
	t.Parallel()
	o := &options{}
	WithSentry("dsn", SentryOptions{
		Setup:                   []sentrykit.Option{sentrykit.WithEnvironment("prod")},
		RefreshGCSlug:           "orders-rgc",
		DisableRefreshGCMonitor: true,
		DisableUserScope:        true,
		ErrorCaptureLevel:       LevelPtr(slog.LevelError),
		Breadcrumbs:             []sentrykit.HandlerOption{sentrykit.WithCaptureDedupeWindow(0)},
	})(o)

	if o.sentryDSN != "dsn" {
		t.Errorf("sentryDSN = %q", o.sentryDSN)
	}
	if len(o.sentryOpts) != 1 {
		t.Errorf("Setup not forwarded: %d entries", len(o.sentryOpts))
	}
	if o.sentryRefreshGCSlug != "orders-rgc" {
		t.Errorf("RefreshGCSlug = %q", o.sentryRefreshGCSlug)
	}
	if !o.skipSentryRefreshGCMonitor {
		t.Error("DisableRefreshGCMonitor did not set skip flag")
	}
	if !o.skipSentryUserScope {
		t.Error("DisableUserScope did not set skip flag")
	}
	// Both ErrorCaptureLevel and Breadcrumbs feed sentrySlogOpts: one
	// for the captureLevel wrapper + one breadcrumb option = 2.
	if len(o.sentrySlogOpts) != 2 {
		t.Errorf("sentrySlogOpts = %d, want 2 (level + breadcrumb)", len(o.sentrySlogOpts))
	}
}

func TestOtelOptions_StructFormPopulatesAllFields(t *testing.T) {
	t.Parallel()
	o := &options{}
	WithOtel("orders", OtelOptions{
		Setup:          []otelkit.Option{otelkit.WithServiceVersion("1.0.0")},
		DisableMetrics: true,
		MetricsOptions: []otelkit.MetricsOption{},
		DisableLogs:    true,
		LogsOptions:    []otelkit.LogsOption{},
	})(o)

	if o.otelServiceName != "orders" {
		t.Errorf("otelServiceName = %q", o.otelServiceName)
	}
	if len(o.otelOpts) != 1 {
		t.Errorf("otel Setup not forwarded: %d entries", len(o.otelOpts))
	}
	if !o.skipOtelMetrics {
		t.Error("DisableMetrics did not set skip flag")
	}
	if !o.skipOtelLogs {
		t.Error("DisableLogs did not set skip flag")
	}
}

func TestLevelPtr_RoundTrip(t *testing.T) {
	t.Parallel()
	p := LevelPtr(slog.LevelError)
	if *p != slog.LevelError {
		t.Errorf("LevelPtr round-trip: got %v, want %v", *p, slog.LevelError)
	}
}
