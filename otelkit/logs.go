package otelkit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/sdk/log"
)

// SetupLogs initialises an OTLP/HTTP log exporter + LoggerProvider
// and registers it as the process-global provider so subsequent
// [SlogHandler] calls bind to it automatically.
//
// Returns a shutdown function the caller registers with their
// cleanup path — typically `service.OnShutdown(shutdown)`. The
// shutdown flushes the in-memory batch via BatchProcessor.Shutdown
// under the provided ctx; cap the ctx with a finite deadline so a
// stalled collector doesn't block process exit indefinitely.
//
// Configuration follows OTel spec exactly — endpoint, headers,
// compression come from `OTEL_EXPORTER_OTLP_LOGS_ENDPOINT` /
// `OTEL_EXPORTER_OTLP_HEADERS` env vars; no kit-specific knobs.
// Resource attributes from `OTEL_RESOURCE_ATTRIBUTES` apply.
//
// Pass at minimum a serviceName — populates `service.name` on every
// emitted record so the OTel collector can route by service.
//
//	shutdown, err := otelkit.SetupLogs(ctx, "urlshort",
//	    otelkit.WithLogsServiceVersion("1.0.0"))
//
// service.WithOtel auto-runs SetupLogs alongside traces + metrics
// when [WithOtelLogs] is also set — most kit users never call this
// directly.
func SetupLogs(ctx context.Context, serviceName string, opts ...LogsOption) (func(context.Context) error, error) {
	if serviceName == "" {
		return nil, errors.New("otelkit: SetupLogs requires non-empty serviceName")
	}
	cfg := logsConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}
	exporter, err := otlploghttp.New(ctx, cfg.exporterOpts...)
	if err != nil {
		return nil, fmt.Errorf("otelkit logs: exporter: %w", err)
	}
	resource, err := buildResource(serviceName, cfg.serviceVersion, cfg.resourceAttrs)
	if err != nil {
		return nil, err
	}
	provider := log.NewLoggerProvider(
		log.WithProcessor(log.NewBatchProcessor(exporter)),
		log.WithResource(resource),
	)
	global.SetLoggerProvider(provider)

	var once sync.Once
	shutdown := func(ctx context.Context) error {
		var err error
		once.Do(func() {
			if ctx == nil {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
			}
			err = provider.Shutdown(ctx)
		})
		return err
	}
	return shutdown, nil
}

// LogsOption tunes [SetupLogs].
type LogsOption func(*logsConfig)

type logsConfig struct {
	serviceVersion string
	resourceAttrs  []kv
	exporterOpts   []otlploghttp.Option
}

// WithLogsServiceVersion sets the `service.version` resource attr
// on the emitted records.
func WithLogsServiceVersion(v string) LogsOption {
	return func(c *logsConfig) { c.serviceVersion = v }
}

// WithLogsResourceAttribute appends a static resource attribute to
// every emitted record. Use for `deployment.environment`,
// `service.namespace`, region, az — anything constant per-process.
func WithLogsResourceAttribute(key, value string) LogsOption {
	return func(c *logsConfig) {
		c.resourceAttrs = append(c.resourceAttrs, kv{key: key, value: value})
	}
}

// WithLogsExporterOption forwards an `otlploghttp.Option` to the
// underlying exporter constructor — endpoint, headers, compression,
// TLS config, retry policy. Use only when the standard
// `OTEL_EXPORTER_OTLP_LOGS_*` env vars aren't expressive enough.
func WithLogsExporterOption(opt otlploghttp.Option) LogsOption {
	return func(c *logsConfig) { c.exporterOpts = append(c.exporterOpts, opt) }
}

// SlogHandler wraps inner with a tee-handler that ALSO emits each
// record via the OTel logs SDK using the process-global
// LoggerProvider (installed by [SetupLogs]). Inner keeps receiving
// records unchanged — console/JSON logging continues to work, OTel
// gets a parallel copy.
//
// When [SetupLogs] hasn't run, the OTel side is a no-op
// (`global.GetLoggerProvider()` returns a no-op provider) so
// callers can wire `SlogHandler` unconditionally — the kit absorbs
// the missing-provider case the same way `sentrykit.SlogHandler`
// does for missing Sentry init.
//
// scopeName is the OTel "instrumentation scope" — typically the
// service name or the package path emitting the record. Defaults
// to the package-path of otelkit when empty.
func SlogHandler(inner slog.Handler, scopeName string) slog.Handler {
	if inner == nil {
		inner = slog.Default().Handler()
	}
	if scopeName == "" {
		scopeName = "github.com/theizzatbek/gokit/otelkit"
	}
	return &teeSlogHandler{
		inner: inner,
		otel:  otelslog.NewHandler(scopeName),
	}
}

// teeSlogHandler dispatches every Record to two handlers in
// sequence. Errors aggregate via errors.Join so neither sink masks
// the other. Enabled returns true if EITHER child is enabled — the
// inner handler controls console-level filtering, the OTel side
// usually accepts everything for collector-side filtering.
type teeSlogHandler struct {
	inner slog.Handler
	otel  slog.Handler
}

func (h *teeSlogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level) || h.otel.Enabled(ctx, level)
}

func (h *teeSlogHandler) Handle(ctx context.Context, r slog.Record) error {
	var errs []error
	if h.inner.Enabled(ctx, r.Level) {
		if err := h.inner.Handle(ctx, r.Clone()); err != nil {
			errs = append(errs, err)
		}
	}
	if h.otel.Enabled(ctx, r.Level) {
		if err := h.otel.Handle(ctx, r.Clone()); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (h *teeSlogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &teeSlogHandler{
		inner: h.inner.WithAttrs(attrs),
		otel:  h.otel.WithAttrs(attrs),
	}
}

func (h *teeSlogHandler) WithGroup(name string) slog.Handler {
	return &teeSlogHandler{
		inner: h.inner.WithGroup(name),
		otel:  h.otel.WithGroup(name),
	}
}
