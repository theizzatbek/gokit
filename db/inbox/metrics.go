package inbox

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// metricsCollector bundles the two inbox_* series and implements
// prometheus.Collector so the registry holds one collector rather
// than two loose vecs.
type metricsCollector struct {
	processed *prometheus.CounterVec // labels: consumer, outcome
	duration  *prometheus.HistogramVec
}

func newMetricsCollector(reg prometheus.Registerer) *metricsCollector {
	m := &metricsCollector{
		processed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "inbox_processed_total",
			Help: "Per-consumer Process outcomes (processed|duplicate|error).",
		}, []string{"consumer", "outcome"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "inbox_process_duration_seconds",
			Help:    "Wall time of one Process call (Tx open → commit/rollback).",
			Buckets: prometheus.DefBuckets,
		}, []string{"consumer"}),
	}
	reg.MustRegister(m)
	return m
}

func (m *metricsCollector) Describe(ch chan<- *prometheus.Desc) {
	m.processed.Describe(ch)
	m.duration.Describe(ch)
}

func (m *metricsCollector) Collect(ch chan<- prometheus.Metric) {
	m.processed.Collect(ch)
	m.duration.Collect(ch)
}

func (m *metricsCollector) observe(consumer, outcome string, dur time.Duration) {
	if m == nil {
		return
	}
	m.processed.WithLabelValues(consumer, outcome).Inc()
	m.duration.WithLabelValues(consumer).Observe(dur.Seconds())
}
