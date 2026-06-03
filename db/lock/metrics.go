package lock

import "github.com/prometheus/client_golang/prometheus"

// outcome labels for lock_acquires_total.
const (
	outcomeAcquired  = "acquired"
	outcomeContended = "contended"
	outcomeError     = "error"
)

// metricsCollector bundles every lock_* series and implements
// prometheus.Collector so the caller's Registerer holds one collector
// rather than two loose vecs.
type metricsCollector struct {
	name     string
	acquires *prometheus.CounterVec // label: outcome=acquired|contended|error
	hold     prometheus.Histogram
}

func newMetricsCollector(reg prometheus.Registerer, name string) *metricsCollector {
	m := &metricsCollector{
		name: name,
		acquires: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "lock_acquires_total",
			Help:        "Advisory-lock acquire attempts by outcome (acquired|contended|error).",
			ConstLabels: prometheus.Labels{"name": name},
		}, []string{"outcome"}),
		hold: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:        "lock_hold_duration_seconds",
			Help:        "Duration the advisory lock was held, from acquire to release.",
			ConstLabels: prometheus.Labels{"name": name},
			// Locks typically wrap quick critical sections OR long jobs.
			// Buckets span both — 1ms to 10min — to keep both ends
			// observable on the same series.
			Buckets: []float64{
				0.001, 0.005, 0.025, 0.1, 0.5, 1, 5, 30, 120, 600,
			},
		}),
	}
	reg.MustRegister(m)
	return m
}

func (m *metricsCollector) Describe(ch chan<- *prometheus.Desc) {
	m.acquires.Describe(ch)
	m.hold.Describe(ch)
}

func (m *metricsCollector) Collect(ch chan<- prometheus.Metric) {
	m.acquires.Collect(ch)
	m.hold.Collect(ch)
}

func (m *metricsCollector) recordOutcome(outcome string) {
	if m == nil {
		return
	}
	m.acquires.WithLabelValues(outcome).Inc()
}

func (m *metricsCollector) observeHold(seconds float64) {
	if m == nil {
		return
	}
	m.hold.Observe(seconds)
}
