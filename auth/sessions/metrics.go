package sessions

import "github.com/prometheus/client_golang/prometheus"

// metrics is the registered set of Prometheus collectors for *Manager.
// nil = no-op (every helper returns early on nil receiver) so the hot
// path stays branch-free when [WithMetrics] is not wired.
type metrics struct {
	ops      *prometheus.CounterVec   // op, outcome
	duration *prometheus.HistogramVec // op
}

func newMetrics(reg prometheus.Registerer) *metrics {
	m := &metrics{
		ops: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "sessions",
			Name:      "ops_total",
			Help:      "Session Manager operations, by op (issue|logout|logout_all|middleware|revoke) and outcome (ok|error; middleware also: missing|invalid|expired|claims_decode).",
		}, []string{"op", "outcome"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "sessions",
			Name:      "op_duration_seconds",
			Help:      "Wall-clock latency of Session Manager operations.",
			Buckets:   prometheus.ExponentialBuckets(0.0005, 2, 12),
		}, []string{"op"}),
	}
	reg.MustRegister(m.ops, m.duration)
	return m
}

func (m *metrics) inc(op, outcome string) {
	if m == nil {
		return
	}
	m.ops.WithLabelValues(op, outcome).Inc()
}

func (m *metrics) observe(op string, seconds float64) {
	if m == nil {
		return
	}
	m.duration.WithLabelValues(op).Observe(seconds)
}
