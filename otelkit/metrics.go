package otelkit

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	otelprombridge "go.opentelemetry.io/contrib/bridges/prometheus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// MetricsOption configures [SetupMetrics].
type MetricsOption func(*metricsConfig)

type metricsConfig struct {
	serviceVersion string
	resourceAttrs  []kv
	exporterOpts   []otlpmetrichttp.Option
	interval       time.Duration
}

// WithMetricsServiceVersion sets service.version on the metric
// resource. Equivalent to [WithServiceVersion] on the trace side —
// kept separate so a service can ship traces and metrics from
// different versions during a rollout if it wants to.
func WithMetricsServiceVersion(v string) MetricsOption {
	return func(c *metricsConfig) { c.serviceVersion = v }
}

// WithMetricsResourceAttribute appends a constant attribute to the
// metric resource. Use for deploy-time facts (region, az, cluster).
func WithMetricsResourceAttribute(key, value string) MetricsOption {
	return func(c *metricsConfig) {
		c.resourceAttrs = append(c.resourceAttrs, kv{key, value})
	}
}

// WithMetricsExporterOption forwards an otlpmetrichttp option (e.g.
// explicit endpoint, insecure mode, custom headers). Most deployments
// drive these via standard OTEL_EXPORTER_OTLP_* env vars.
func WithMetricsExporterOption(opt otlpmetrichttp.Option) MetricsOption {
	return func(c *metricsConfig) { c.exporterOpts = append(c.exporterOpts, opt) }
}

// WithMetricsInterval overrides the PeriodicReader push interval.
// Default 60s (the OTel spec's recommendation for backend-stable
// push). Lower values increase fidelity at the cost of bandwidth +
// collector load.
func WithMetricsInterval(d time.Duration) MetricsOption {
	return func(c *metricsConfig) { c.interval = d }
}

// SetupMetrics wires the process-global MeterProvider so the kit's
// Prometheus collectors (`db_*`, `httpc_*`, `nats_*`, `apimap_*`,
// `auth_*`, `fibermap_http_*`, plus app-registered series) are
// periodically read from `reg` and pushed via OTLP/HTTP.
//
//	shutdown, err := otelkit.SetupMetrics(ctx, "urlshort", svc.Metrics().(prometheus.Gatherer),
//	    otelkit.WithMetricsInterval(30*time.Second))
//
// The bridge approach lets the kit keep its existing Prometheus
// instrumentation while still emitting OTel-shaped metrics to a
// unified collector (Tempo+Mimir, Honeycomb, Datadog, …). No
// rewriting of subsystem instrumentation is required.
//
// serviceName populates service.name on every metric resource and is
// required — empty values trip an error so misconfigured services
// fail loud at boot. reg must be non-nil; without a Gatherer there
// is nothing to bridge.
//
// The returned shutdown function flushes pending metrics and tears
// down the exporter. Bound the context with a finite deadline before
// calling it — an unresponsive collector otherwise blocks shutdown.
//
// SetupMetrics is idempotent on the kit side (sync.Once-guarded
// shutdown) but the global SetMeterProvider is not — calling
// SetupMetrics twice replaces the previous provider. The kit's
// subsystems hold no Meter references, so a second SetupMetrics call
// affects only ad-hoc Meters callers obtained after the swap.
func SetupMetrics(ctx context.Context, serviceName string, reg prometheus.Gatherer, opts ...MetricsOption) (func(context.Context) error, error) {
	if serviceName == "" {
		return nil, errors.New("otelkit: SetupMetrics serviceName is required")
	}
	if reg == nil {
		return nil, errors.New("otelkit: SetupMetrics needs a non-nil prometheus.Gatherer (bridge source)")
	}
	cfg := &metricsConfig{interval: 60 * time.Second}
	for _, opt := range opts {
		opt(cfg)
	}

	exp, err := otlpmetrichttp.New(ctx, cfg.exporterOpts...)
	if err != nil {
		return nil, fmt.Errorf("otelkit: build otlp metric exporter: %w", err)
	}

	res, err := buildResource(serviceName, cfg.serviceVersion, cfg.resourceAttrs)
	if err != nil {
		_ = exp.Shutdown(ctx)
		return nil, fmt.Errorf("otelkit: build metric resource: %w", err)
	}

	bridge := otelprombridge.NewMetricProducer(otelprombridge.WithGatherer(reg))
	reader := sdkmetric.NewPeriodicReader(exp,
		sdkmetric.WithInterval(cfg.interval),
		sdkmetric.WithProducer(bridge),
	)

	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(reader),
		sdkmetric.WithResource(res),
	)
	otel.SetMeterProvider(mp)

	var once sync.Once
	return func(shutdownCtx context.Context) error {
		var err error
		once.Do(func() {
			err = mp.Shutdown(shutdownCtx)
		})
		return err
	}, nil
}