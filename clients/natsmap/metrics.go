package natsmap

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// natsmapMetrics is the natsmap-owned collector set. Created at
// Build time when [WithMetrics] is supplied; nil otherwise.
//
// Naming + cardinality choices:
//
//   - `name` is the YAML-declared subscriber/publisher identity, so
//     dashboards key on stable kit constants rather than ad-hoc
//     subjects.
//   - `outcome` is bounded: `success` / `error` for both handlers and
//     publishes. Handler panics flow through after-hook as `error`;
//     transport failures on publish surface the same way.
//
// Subscription-level metrics (per-subject `nats_handler_total`,
// `nats_publish_total`) stay on clients/nats — these counters are the
// declarative-layer counterparts.
type natsmapMetrics struct {
	handlersTotal   *prometheus.CounterVec
	handlerDuration *prometheus.HistogramVec
	publishesTotal  *prometheus.CounterVec
}

func newNatsmapMetrics(reg prometheus.Registerer) *natsmapMetrics {
	m := &natsmapMetrics{
		handlersTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "natsmap_handlers_total",
			Help: "Number of natsmap subscriber dispatches by declared name and outcome.",
		}, []string{"name", "outcome"}),
		handlerDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "natsmap_handler_duration_seconds",
			Help:    "Handler wall-clock duration, measured around the user fn.",
			Buckets: prometheus.DefBuckets,
		}, []string{"name"}),
		publishesTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "natsmap_publishes_total",
			Help: "Number of natsmap publish calls by declared name and outcome.",
		}, []string{"name", "outcome"}),
	}
	reg.MustRegister(m.handlersTotal, m.handlerDuration, m.publishesTotal)
	return m
}

func (m *natsmapMetrics) observeHandler(name, outcome string, d time.Duration) {
	if m == nil {
		return
	}
	m.handlersTotal.WithLabelValues(name, outcome).Inc()
	m.handlerDuration.WithLabelValues(name).Observe(d.Seconds())
}

func (m *natsmapMetrics) observePublish(name, outcome string) {
	if m == nil {
		return
	}
	m.publishesTotal.WithLabelValues(name, outcome).Inc()
}
