package service

import (
	"log/slog"

	"github.com/theizzatbek/gokit/otelkit"
	"github.com/theizzatbek/gokit/sentrykit"
)

// SentryOptions groups every Sentry-side knob that used to live in
// six separate With*/Without* options. See [WithSentry].
//
// The DSN stays as a positional parameter on WithSentry — empty DSN
// is the universal "disable Sentry" signal in this kit, so making it
// a struct field that defaults to "" would be ambiguous with "enable
// Sentry with no DSN" (which sentrykit.Setup rejects).
//
// All fields are optional; the zero value carries the same defaults
// the deprecated leaf-options used to apply individually.
type SentryOptions struct {
	// Setup is forwarded verbatim to [sentrykit.Setup]. Use for env,
	// release tag, sample ratio, tags, etc.
	Setup []sentrykit.Option

	// RefreshGCSlug overrides the default "kit-refresh-gc" monitor
	// slug used by the refresh-token GC Sentry-Crons check-in. Empty
	// = use default. No effect unless [WithRefreshGC] is also set.
	RefreshGCSlug string

	// DisableRefreshGCMonitor suppresses Sentry-Crons check-ins for
	// the refresh-token GC ticker. Tracing / breadcrumbs / error
	// capture stay on; only the periodic check-in is skipped.
	DisableRefreshGCMonitor bool

	// DisableUserScope suppresses the per-request `sentry.User{ID:
	// principal.Subject}` scope that service auto-installs when Auth
	// is wired. Set when Subject is PII in your deployment.
	DisableUserScope bool

	// ErrorCaptureLevel enables Sentry-event auto-capture for log
	// records at or above the level. nil = no capture (default).
	// Pass `service.LevelPtr(slog.LevelError)` for the typical setup.
	ErrorCaptureLevel *slog.Level

	// Breadcrumbs configures the slog→breadcrumb bridge that service
	// auto-installs on the kit-built logger. Forwarded to
	// [sentrykit.SlogHandler].
	Breadcrumbs []sentrykit.HandlerOption
}

// OtelOptions groups every OTel-side knob that used to live in five
// separate With*/Without* options. See [WithOtel].
//
// ServiceName remains a positional parameter on WithOtel for the same
// reason as Sentry's DSN.
type OtelOptions struct {
	// Setup is forwarded verbatim to [otelkit.Setup] (tracing config:
	// sample ratio, service version, resource attributes, exporter
	// overrides).
	Setup []otelkit.Option

	// DisableMetrics suppresses the Prometheus→OTel metrics bridge
	// that WithOtel otherwise auto-enables. Tracing stays on.
	DisableMetrics bool

	// MetricsOptions configures the OTel metrics pipeline. Forwarded
	// to [otelkit.SetupMetrics]. Ignored when DisableMetrics is true.
	MetricsOptions []otelkit.MetricsOption

	// DisableLogs suppresses the slog→OTel log bridge that WithOtel
	// otherwise auto-enables. Tracing and metrics stay on.
	DisableLogs bool

	// LogsOptions configures the OTel log pipeline. Forwarded to
	// [otelkit.SetupLogs]. Ignored when DisableLogs is true.
	LogsOptions []otelkit.LogsOption
}

// LevelPtr is a small convenience helper for the common case of
// passing a slog.Level into [SentryOptions.ErrorCaptureLevel] without
// declaring an intermediate variable:
//
//	service.SentryOptions{ErrorCaptureLevel: service.LevelPtr(slog.LevelError)}
func LevelPtr(l slog.Level) *slog.Level { return &l }

// applySentryOptions copies SentryOptions into the internal options
// struct. Shared between the new struct-form WithSentry and the
// deprecated leaf-options so behaviour stays identical.
func applySentryOptions(o *options, opts SentryOptions) {
	o.sentryOpts = append(o.sentryOpts, opts.Setup...)
	if opts.RefreshGCSlug != "" {
		o.sentryRefreshGCSlug = opts.RefreshGCSlug
	}
	if opts.DisableRefreshGCMonitor {
		o.skipSentryRefreshGCMonitor = true
	}
	if opts.DisableUserScope {
		o.skipSentryUserScope = true
	}
	if opts.ErrorCaptureLevel != nil {
		o.sentrySlogOpts = append(o.sentrySlogOpts,
			sentrykit.WithCaptureLevel(*opts.ErrorCaptureLevel))
	}
	o.sentrySlogOpts = append(o.sentrySlogOpts, opts.Breadcrumbs...)
}

// applyOtelOptions copies OtelOptions into the internal options
// struct. Shared between the new struct-form WithOtel and the
// deprecated leaf-options.
func applyOtelOptions(o *options, opts OtelOptions) {
	o.otelOpts = append(o.otelOpts, opts.Setup...)
	if opts.DisableMetrics {
		o.skipOtelMetrics = true
	}
	o.otelMetricsOpts = append(o.otelMetricsOpts, opts.MetricsOptions...)
	if opts.DisableLogs {
		o.skipOtelLogs = true
	}
	o.otelLogsOpts = append(o.otelLogsOpts, opts.LogsOptions...)
}
