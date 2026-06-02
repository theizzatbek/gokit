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

func TestSentryOptions_DeprecatedWrappersStillWork(t *testing.T) {
	t.Parallel()
	o := &options{}
	// Old call-site shape: WithSentry(dsn, empty) + leaf options.
	WithSentry("dsn", SentryOptions{})(o)
	WithSentryRefreshGCSlug("legacy")(o)
	WithoutSentryRefreshGCMonitor()(o)
	WithoutSentryUserScope()(o)
	WithSentryErrorCapture(slog.LevelWarn)(o)
	WithSentryBreadcrumbs(sentrykit.WithCaptureDedupeWindow(0))(o)

	if o.sentryRefreshGCSlug != "legacy" {
		t.Errorf("legacy RefreshGCSlug not applied: %q", o.sentryRefreshGCSlug)
	}
	if !o.skipSentryRefreshGCMonitor || !o.skipSentryUserScope {
		t.Error("legacy disable flags not applied")
	}
	if len(o.sentrySlogOpts) != 2 {
		t.Errorf("legacy slog opts = %d, want 2", len(o.sentrySlogOpts))
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

func TestOtelOptions_DeprecatedWrappersStillWork(t *testing.T) {
	t.Parallel()
	o := &options{}
	WithOtel("svc", OtelOptions{})(o)
	WithoutOtelMetrics()(o)
	WithoutOtelLogs()(o)
	WithOtelMetricsOptions()(o)
	WithOtelLogsOptions()(o)

	if !o.skipOtelMetrics {
		t.Error("WithoutOtelMetrics legacy path did not flip skip flag")
	}
	if !o.skipOtelLogs {
		t.Error("WithoutOtelLogs legacy path did not flip skip flag")
	}
}

func TestSentryOptions_MixedStructAndLegacyComposable(t *testing.T) {
	t.Parallel()
	// Real world: caller migrates to the struct form gradually and may
	// still have a legacy leaf-option lying around. The two paths must
	// not stomp on each other — the leaf option appends, doesn't reset.
	o := &options{}
	WithSentry("dsn", SentryOptions{
		Setup: []sentrykit.Option{sentrykit.WithEnvironment("prod")},
	})(o)
	WithSentryErrorCapture(slog.LevelError)(o)

	if len(o.sentryOpts) != 1 {
		t.Errorf("Setup count = %d, want 1", len(o.sentryOpts))
	}
	if len(o.sentrySlogOpts) != 1 {
		t.Errorf("slogOpts = %d, want 1 (from legacy capture)", len(o.sentrySlogOpts))
	}
}

func TestLevelPtr_RoundTrip(t *testing.T) {
	t.Parallel()
	p := LevelPtr(slog.LevelError)
	if *p != slog.LevelError {
		t.Errorf("LevelPtr round-trip: got %v, want %v", *p, slog.LevelError)
	}
}
