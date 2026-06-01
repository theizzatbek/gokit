package jobs

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type workerMetrics struct {
	processed *prometheus.CounterVec
	duration  *prometheus.HistogramVec
	inflight  prometheus.Gauge
	pollErr   prometheus.Counter
}

func newWorkerMetrics(reg prometheus.Registerer) *workerMetrics {
	m := &workerMetrics{
		processed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "jobs_processed_total",
			Help: "Job dispatch outcomes by type and result.",
		}, []string{"type", "outcome"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "jobs_dispatch_duration_seconds",
			Help:    "Time spent inside HandlerFunc (incl. JSON decode).",
			Buckets: []float64{0.001, 0.01, 0.05, 0.1, 0.5, 1, 5, 30, 60},
		}, []string{"type"}),
		inflight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "jobs_inflight",
			Help: "Jobs currently being dispatched by the local Worker.",
		}),
		pollErr: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "jobs_poll_errors_total",
			Help: "Tick-level errors (CLAIM SQL / scan failures).",
		}),
	}
	reg.MustRegister(m.processed, m.duration, m.inflight, m.pollErr)
	return m
}

func (m *workerMetrics) observe(jobType string, d time.Duration, outcome string) {
	if m == nil {
		return
	}
	m.processed.WithLabelValues(jobType, outcome).Inc()
	m.duration.WithLabelValues(jobType).Observe(d.Seconds())
}

func (m *workerMetrics) incInflight() {
	if m == nil {
		return
	}
	m.inflight.Inc()
}

func (m *workerMetrics) decInflight() {
	if m == nil {
		return
	}
	m.inflight.Dec()
}

func (m *workerMetrics) recordPollErr() {
	if m == nil {
		return
	}
	m.pollErr.Inc()
}
