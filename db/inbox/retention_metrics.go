package inbox

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// retentionCollector bundles the three inbox_retention_* series.
// nil-receiver-safe.
type retentionCollector struct {
	rowsDeleted prometheus.Counter
	duration    prometheus.Histogram
	errors      prometheus.Counter
}

func newRetentionCollector(reg prometheus.Registerer) *retentionCollector {
	m := &retentionCollector{
		rowsDeleted: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "inbox_retention_rows_deleted_total",
			Help: "Total rows deleted by the retention worker.",
		}),
		duration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "inbox_retention_tick_duration_seconds",
			Help:    "Wall time of one retention DELETE tick.",
			Buckets: prometheus.DefBuckets,
		}),
		errors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "inbox_retention_tick_errors_total",
			Help: "Number of retention ticks that failed.",
		}),
	}
	reg.MustRegister(m)
	return m
}

func (m *retentionCollector) Describe(ch chan<- *prometheus.Desc) {
	m.rowsDeleted.Describe(ch)
	m.duration.Describe(ch)
	m.errors.Describe(ch)
}

func (m *retentionCollector) Collect(ch chan<- prometheus.Metric) {
	m.rowsDeleted.Collect(ch)
	m.duration.Collect(ch)
	m.errors.Collect(ch)
}

func (m *retentionCollector) observeOK(rows int64, dur time.Duration) {
	if m == nil {
		return
	}
	m.rowsDeleted.Add(float64(rows))
	m.duration.Observe(dur.Seconds())
}

func (m *retentionCollector) observeError(dur time.Duration) {
	if m == nil {
		return
	}
	m.errors.Inc()
	m.duration.Observe(dur.Seconds())
}
