package fibermap

import (
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/adaptor"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics returns a Fiber middleware that records Prometheus metrics
// for every request, plus the *prometheus.Registry the metrics live
// on. Three series are exported:
//
//   - `fibermap_http_requests_total{method,route,status}` — counter
//   - `fibermap_http_request_duration_seconds{method,route,status}`
//     — histogram (default buckets)
//   - `fibermap_http_requests_in_flight` — gauge
//
// `route` is the Fiber route template (e.g. `/v1/tasks/:id`), not the
// concrete path — keeps label cardinality bounded. Requests that
// don't match any registered route get an empty `route` label.
//
// Pair with [MetricsHandler] to expose the values at a scrape
// endpoint, or use [WithMetrics] to wire both in one call:
//
//	mw, reg := fibermap.Metrics()
//	app.Use(mw)
//	app.Get("/metrics", fibermap.MetricsHandler(reg))
func Metrics() (fiber.Handler, *prometheus.Registry) {
	reg := prometheus.NewRegistry()
	reqCounter := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "fibermap",
		Subsystem: "http",
		Name:      "requests_total",
		Help:      "Number of HTTP requests served, labelled by method, route template, and status code.",
	}, []string{"method", "route", "status"})
	reqDuration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "fibermap",
		Subsystem: "http",
		Name:      "request_duration_seconds",
		Help:      "HTTP request handler latency in seconds.",
		Buckets:   prometheus.DefBuckets,
	}, []string{"method", "route", "status"})
	inFlight := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "fibermap",
		Subsystem: "http",
		Name:      "requests_in_flight",
		Help:      "Number of HTTP requests currently being processed.",
	})
	reg.MustRegister(reqCounter, reqDuration, inFlight)

	mw := func(c *fiber.Ctx) error {
		inFlight.Inc()
		defer inFlight.Dec()

		start := time.Now()
		err := c.Next()

		status := strconv.Itoa(c.Response().StatusCode())
		method := c.Method()
		route := ""
		if r := c.Route(); r != nil {
			route = r.Path
		}
		reqCounter.WithLabelValues(method, route, status).Inc()
		reqDuration.WithLabelValues(method, route, status).Observe(time.Since(start).Seconds())
		return err
	}
	return mw, reg
}

// MetricsHandler returns a fiber.Handler that exposes `reg`'s metrics
// in Prometheus text format. Mount it under your scrape path —
// typically `/metrics`:
//
//	app.Get("/metrics", fibermap.MetricsHandler(reg))
func MetricsHandler(reg *prometheus.Registry) fiber.Handler {
	return adaptor.HTTPHandler(promhttp.HandlerFor(reg, promhttp.HandlerOpts{
		Registry: reg,
	}))
}
