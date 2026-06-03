package cache

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// metrics bundles the cache-owned collectors. Created in [New] when
// Config.MetricsReg is set (Config.Name then mandatory). Nil
// otherwise; every call site nil-guards.
//
// Labels:
//
//	name      — Config.Name (one per cache instance; bounded cardinality)
//	operation — get | set | set_not_found | invalidate | invalidate_prefix
//	outcome   — hit | miss | negative | ok | error
//
// `name` is required so a single registry can host multiple caches
// (links + users + session ...) without collision. `operation`
// distinguishes read vs write paths; `outcome` separates hits from
// misses for the read path and ok/error for writes.
type metrics struct {
	operations *prometheus.CounterVec
	duration   *prometheus.HistogramVec
	name       string
}

func newMetrics(reg prometheus.Registerer, name string) *metrics {
	m := &metrics{
		name: name,
		operations: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cache_operations_total",
			Help: "Number of cache operations by instance name, operation, and outcome.",
		}, []string{"name", "operation", "outcome"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "cache_operation_duration_seconds",
			Help:    "Cache operation wall-clock duration in seconds.",
			Buckets: prometheus.DefBuckets,
		}, []string{"name", "operation"}),
	}
	reg.MustRegister(m.operations, m.duration)
	return m
}

func (m *metrics) observe(operation, outcome string, elapsed time.Duration) {
	if m == nil {
		return
	}
	m.operations.WithLabelValues(m.name, operation, outcome).Inc()
	m.duration.WithLabelValues(m.name, operation).Observe(elapsed.Seconds())
}
