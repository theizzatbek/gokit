// Package otelkit is the kit's thin OpenTelemetry tracing bootstrap.
//
// One call sets up a TracerProvider exporting via OTLP/HTTP, wires it
// as the process-global TracerProvider + Propagator, and returns a
// shutdown function callers register with their cleanup path
// (service.OnShutdown is the canonical home).
//
//	shutdown, err := otelkit.Setup(ctx, "urlshort")
//	if err != nil { return err }
//	defer shutdown(context.Background())
//
// Environment configuration follows the OTel spec exactly — endpoint,
// headers, compression, and resource attributes are read from
// OTEL_EXPORTER_OTLP_* and OTEL_RESOURCE_ATTRIBUTES. service.WithOtel
// wraps this plus otelfiber + otelhttp transport wiring in a single
// Service option.
//
// Scope of v1: traces only. Metrics + logs pipelines are out of scope;
// add them via the same exporter once the kit's metrics story stops
// going through Prometheus.
package otelkit

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// Option configures Setup.
type Option func(*config)

type config struct {
	serviceVersion string
	sampleRatio    float64
	resourceAttrs  []kv
	exporterOpts   []otlptracehttp.Option
}

type kv struct{ key, value string }

// WithServiceVersion sets service.version on the trace resource —
// flows into every span. Defaults to empty; sets the attribute only
// when supplied.
func WithServiceVersion(v string) Option {
	return func(c *config) { c.serviceVersion = v }
}

// WithSampleRatio sets the head-based sampling ratio (0..1). Default
// is 1.0 (sample everything). Use < 1.0 for high-traffic services
// where storing every span is impractical.
func WithSampleRatio(r float64) Option {
	return func(c *config) { c.sampleRatio = r }
}

// WithResourceAttribute appends a constant attribute to the resource.
// Useful for deploy-time facts (region, az, cluster) that aren't part
// of the kit's standard set.
func WithResourceAttribute(key, value string) Option {
	return func(c *config) {
		c.resourceAttrs = append(c.resourceAttrs, kv{key, value})
	}
}

// WithExporterOption forwards an otlptracehttp option (e.g. explicit
// endpoint or insecure mode). Most deployments rely on
// OTEL_EXPORTER_OTLP_ENDPOINT and friends; this is the escape hatch.
func WithExporterOption(opt otlptracehttp.Option) Option {
	return func(c *config) { c.exporterOpts = append(c.exporterOpts, opt) }
}

// Setup initializes the process-global tracer provider + propagator
// and returns a shutdown function. serviceName populates
// service.name on every span and MUST be supplied — empty values
// trip an error so misconfigured services fail loud at boot.
//
// The returned shutdown function flushes pending spans and tears down
// the exporter. Bound a context with a finite deadline before calling
// it; an unresponsive collector otherwise blocks shutdown forever.
//
// Setup is idempotent on the kit side but the global SetTracerProvider
// is not — calling Setup twice replaces the previous provider, so
// downstream code holding a Tracer obtained before the second call
// keeps emitting through the old pipeline until shutdown.
func Setup(ctx context.Context, serviceName string, opts ...Option) (func(context.Context) error, error) {
	if serviceName == "" {
		return nil, errors.New("otelkit: serviceName is required")
	}
	cfg := &config{sampleRatio: 1.0}
	for _, opt := range opts {
		opt(cfg)
	}

	exp, err := otlptrace.New(ctx, otlptracehttp.NewClient(cfg.exporterOpts...))
	if err != nil {
		return nil, fmt.Errorf("otelkit: build otlp exporter: %w", err)
	}

	attrs := []attribute.KeyValue{
		semconv.ServiceName(serviceName),
	}
	if cfg.serviceVersion != "" {
		attrs = append(attrs, semconv.ServiceVersion(cfg.serviceVersion))
	}
	for _, a := range cfg.resourceAttrs {
		attrs = append(attrs, attribute.String(a.key, a.value))
	}

	// Use NewSchemaless to avoid clashing with resource.Default's
	// embedded schema URL, then Merge — Merge tolerates a schemaless
	// override layered on top of a schema-attached default.
	res, err := resource.Merge(resource.Default(),
		resource.NewSchemaless(attrs...))
	if err != nil {
		_ = exp.Shutdown(ctx)
		return nil, fmt.Errorf("otelkit: build resource: %w", err)
	}

	sampler := sdktrace.TraceIDRatioBased(cfg.sampleRatio)
	if cfg.sampleRatio >= 1.0 {
		sampler = sdktrace.AlwaysSample()
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp,
			sdktrace.WithBatchTimeout(5*time.Second)),
		sdktrace.WithSampler(sampler),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	var once sync.Once
	return func(shutdownCtx context.Context) error {
		var err error
		once.Do(func() {
			err = tp.Shutdown(shutdownCtx)
		})
		return err
	}, nil
}
