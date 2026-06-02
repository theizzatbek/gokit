package webhooks

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type workerMetrics struct {
	attemptedTotal *prometheus.CounterVec
	inFlight       prometheus.Gauge
	duration       prometheus.Histogram
}

func newWorkerMetrics(reg prometheus.Registerer) *workerMetrics {
	m := &workerMetrics{
		attemptedTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "webhooks_deliveries_attempted_total",
			Help: "Webhook delivery attempts grouped by outcome.",
		}, []string{"outcome"}),
		inFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "webhooks_worker_in_flight",
			Help: "Concurrent in-flight webhook deliveries.",
		}),
		duration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "webhooks_delivery_duration_seconds",
			Help:    "Webhook delivery HTTP RTT.",
			Buckets: prometheus.ExponentialBuckets(0.05, 2, 10),
		}),
	}
	if reg != nil {
		reg.MustRegister(m.attemptedTotal, m.inFlight, m.duration)
	}
	return m
}

func (m *workerMetrics) attempted(outcome string) {
	if m == nil {
		return
	}
	m.attemptedTotal.WithLabelValues(outcome).Inc()
}
func (m *workerMetrics) inFlightInc() {
	if m == nil {
		return
	}
	m.inFlight.Inc()
}
func (m *workerMetrics) inFlightDec() {
	if m == nil {
		return
	}
	m.inFlight.Dec()
}
func (m *workerMetrics) deliveryDuration(d time.Duration) {
	if m == nil {
		return
	}
	m.duration.Observe(d.Seconds())
}
