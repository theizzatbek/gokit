package service

import (
	"context"
	"net/http"
	"time"

	"github.com/gofiber/contrib/otelfiber/v2"
	"github.com/gofiber/fiber/v2"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/theizzatbek/gokit/clients/httpc"
	"github.com/theizzatbek/gokit/otelkit"
)

// setupOtel runs otelkit.Setup when WithOtel was passed, then injects
// otelfiber as the OUTERMOST fiber middleware (so every incoming
// request gets a root span before any other handler runs) and adds an
// otelhttp transport wrap to httpc so outbound calls emit CLIENT spans
// + propagate W3C TraceContext headers.
//
// The returned shutdown is registered via Service.OnShutdown so a
// clean Close flushes pending spans before the process exits. Errors
// during exporter setup return early; the caller's Close path tears
// down whatever subsystems were already built.
func (s *Service[T, C]) setupOtel(ctx context.Context) error {
	if s.opts == nil || s.opts.otelServiceName == "" {
		return nil
	}
	shutdown, err := otelkit.Setup(ctx, s.opts.otelServiceName, s.opts.otelOpts...)
	if err != nil {
		return err
	}
	s.otelShutdown = shutdown

	// Prepend otelfiber so the trace span begins before any user
	// middleware (cors, helmet, custom interceptors, …) gets a chance
	// to short-circuit. Without prepending, an early-return CORS
	// preflight would skip the span altogether. service.name comes
	// from the global TracerProvider's resource — otelfiber doesn't
	// need it set per-middleware.
	s.opts.fiberMiddleware = append(
		[]fiber.Handler{otelfiber.Middleware()},
		s.opts.fiberMiddleware...,
	)

	// Wire otelhttp as httpc's base transport. The retry layer wraps
	// the base, so each retry attempt is its own CLIENT span — which
	// is the convention most APMs (Tempo, Jaeger, Honeycomb) expect.
	s.opts.httpcOpts = append(s.opts.httpcOpts,
		httpc.WithBaseTransport(otelhttp.NewTransport(http.DefaultTransport)))

	// Metrics pipeline: bridge the service-wide Prometheus registry
	// onto OTLP/HTTP so the same /metrics scrape data also lands at
	// the OTel collector. Skipped when the configured Registerer is
	// not also a Gatherer (e.g. caller passed a wrapped registry) —
	// in that case the trace pipeline still runs.
	if g, ok := s.metrics.(prometheus.Gatherer); ok && !s.opts.skipOtelMetrics {
		metricShutdown, err := otelkit.SetupMetrics(ctx, s.opts.otelServiceName, g, s.opts.otelMetricsOpts...)
		if err != nil {
			// Tear down the trace pipeline we already installed so we
			// don't leak a TracerProvider on partial failure.
			_ = shutdown(ctx)
			s.otelShutdown = nil
			return err
		}
		s.otelMetricsShutdown = metricShutdown
	}
	return nil
}

// registerOtelShutdown registers the otelShutdown callback with
// OnShutdown so Close runs it before tearing down DB / NATS. Called
// once after the Service is fully built.
func (s *Service[T, C]) registerOtelShutdown() {
	// Metrics shutdown registers FIRST so it runs LAST (OnShutdown is
	// LIFO). Order matters: we want to flush spans before the metric
	// pipeline tears down — the metrics provider's last push includes
	// any counters the span flush mutated.
	if s.otelMetricsShutdown != nil {
		shutdown := s.otelMetricsShutdown
		s.OnShutdown(func() error {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return shutdown(ctx)
		})
	}
	if s.otelShutdown != nil {
		shutdown := s.otelShutdown
		s.OnShutdown(func() error {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return shutdown(ctx)
		})
	}
}
