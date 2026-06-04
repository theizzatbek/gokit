package refreshpg

import "github.com/prometheus/client_golang/prometheus"

// metrics is the registered set of Prometheus collectors for *Store.
// Constructed by [newMetrics] only when [WithMetrics] is wired; nil
// otherwise — every increment helper is a no-op (nil receiver) so
// the hot path stays branch-free for callers without observability.
type metrics struct {
	ops      *prometheus.CounterVec   // op, outcome
	duration *prometheus.HistogramVec // op
}

func newMetrics(reg prometheus.Registerer) *metrics {
	m := &metrics{
		ops: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "refreshpg",
			Name:      "ops_total",
			Help:      "Refresh-token store operations, by op (issue|consume|revoke_family|revoke_subject|revoke_ip|gc|stats|list) and outcome (ok|error; consume also: missing|expired|reused).",
		}, []string{"op", "outcome"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "refreshpg",
			Name:      "op_duration_seconds",
			Help:      "Wall-clock latency of refresh-token store operations.",
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
