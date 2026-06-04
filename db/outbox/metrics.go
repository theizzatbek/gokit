package outbox

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// outcome labels for the events_total counter. Stable across
// versions — dashboards can switch on them safely.
const (
	outcomeSuccess    = "success"
	outcomeFailure    = "failure"
	outcomeDeadLetter = "dead_letter"
	outcomeGC         = "gc_deleted"
)

// metricsCollector bundles the worker's Prometheus collectors.
// Constructed lazily inside [NewWorker] when [WithMetrics] is
// passed; nil-safe — every record method short-circuits on nil
// receiver so the hot path stays zero-cost when metrics are off.
type metricsCollector struct {
	eventsTotal     *prometheus.CounterVec
	publishDuration prometheus.Histogram
	pendingCount    prometheus.Gauge
	gcDeletedTotal  prometheus.Counter
}

// newMetricsCollector registers the kit-named collectors on reg.
// Returns nil when reg is nil so callers can pass an absent
// Registerer without a guard.
func newMetricsCollector(reg prometheus.Registerer) *metricsCollector {
	if reg == nil {
		return nil
	}
	m := &metricsCollector{
		eventsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "outbox_events_total",
				Help: "Outbox event dispatch outcomes by terminal state.",
			},
			[]string{"outcome"},
		),
		publishDuration: prometheus.NewHistogram(
			prometheus.HistogramOpts{
				Name:    "outbox_publish_duration_seconds",
				Help:    "Per-event PublishFn wall time.",
				Buckets: prometheus.DefBuckets,
			},
		),
		pendingCount: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "outbox_pending_count",
				Help: "Rows in outbox awaiting dispatch (published_at IS NULL AND next_retry_at <= NOW()).",
			},
		),
		gcDeletedTotal: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "outbox_gc_deleted_total",
				Help: "Cumulative rows removed by the retention GC.",
			},
		),
	}
	reg.MustRegister(
		m.eventsTotal,
		m.publishDuration,
		m.pendingCount,
		m.gcDeletedTotal,
	)
	return m
}

// recordOutcome increments outbox_events_total for the supplied
// terminal state. No-op on nil receiver.
func (m *metricsCollector) recordOutcome(outcome string) {
	if m == nil {
		return
	}
	m.eventsTotal.WithLabelValues(outcome).Inc()
}

// observePublish records PublishFn duration.
func (m *metricsCollector) observePublish(d time.Duration) {
	if m == nil {
		return
	}
	m.publishDuration.Observe(d.Seconds())
}

// setPending updates the pending-count gauge.
func (m *metricsCollector) setPending(n int) {
	if m == nil {
		return
	}
	m.pendingCount.Set(float64(n))
}

// recordGC increments the retention-GC counter by n.
func (m *metricsCollector) recordGC(n int64) {
	if m == nil || n <= 0 {
		return
	}
	m.gcDeletedTotal.Add(float64(n))
}

// refreshPending issues `SELECT count(*) FROM outbox WHERE ...` to
// update the gauge. Called from the worker tick under the same ctx
// as the drain — a slow count blocks the next drain. Use sparingly.
func (m *metricsCollector) refreshPending(ctx context.Context, w *Worker) {
	if m == nil {
		return
	}
	var n int
	const sql = `SELECT count(*) FROM outbox WHERE published_at IS NULL AND next_retry_at <= NOW()`
	if err := w.db.QueryRow(ctx, sql).Scan(&n); err != nil {
		return
	}
	m.setPending(n)
}
