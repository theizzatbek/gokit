package db

import (
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

// poolStat is the subset of pgxpool.Stat we expose as gauges. Defined
// separately so unit tests can populate it without spinning up a pool.
type poolStat struct {
	Acquired int32
	Idle     int32
	Max      int32
	Total    int32
}

// metricsCollector implements prometheus.Collector. Gauges are refreshed
// from the underlying pgxpool on every scrape — no goroutine, no polling.
// The duration histogram is observed inline by the tracer.
//
// pools is keyed by a stable name ("primary", "standby") emitted as the
// pool="…" label on db_pool_size_total. attach is called once per pool
// during Connect; no locking because there are no concurrent attaches and
// the map is sealed before the first scrape.
type metricsCollector struct {
	pools           map[string]*pgxpool.Pool
	poolSize        *prometheus.GaugeVec
	duration        *prometheus.HistogramVec
	txTotal         *prometheus.CounterVec   // kind=tx|savepoint, outcome=commit|rollback|panic
	txLatency       *prometheus.HistogramVec // kind, outcome
	slowQuery       prometheus.Counter
	txRetries       prometheus.Counter
	replicaLag      *prometheus.GaugeVec   // pool="standby[-N]"
	replicaSkipped  *prometheus.CounterVec // pool, reason=unhealthy|over_budget
	replicaFallback prometheus.Counter
}

func newMetricsCollector(reg prometheus.Registerer) *metricsCollector {
	mc := &metricsCollector{
		pools: map[string]*pgxpool.Pool{},
		poolSize: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "db_pool_size_total",
			Help: "pgx pool size, labelled by pool (primary|standby) and state (acquired|idle|max|total).",
		}, []string{"pool", "state"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "db_query_duration_seconds",
			Help:    "Histogram of pgx query durations. The `name` label is populated when ctx carries [WithQueryName] — empty otherwise; operators must keep the name set bounded.",
			Buckets: prometheus.DefBuckets,
		}, []string{"name", "outcome"}),
		txTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "db_tx_total",
			Help: "Number of completed transactions/savepoints by kind (tx=top-level BEGIN, savepoint=nested) and outcome (commit, rollback from returned error, panic recovered + re-thrown).",
		}, []string{"kind", "outcome"}),
		txLatency: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "db_tx_duration_seconds",
			Help:    "Wall time spent inside DB.Tx / Tx.Tx callbacks, measured from BeginTx to Commit/Rollback completion.",
			Buckets: prometheus.DefBuckets,
		}, []string{"kind", "outcome"}),
		slowQuery: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "db_slow_query_total",
			Help: "Number of queries whose execution exceeded the configured slow-query threshold (WithSlowQueryThreshold). Errored queries do NOT count here — they show up in db_query_duration_seconds{outcome=error} instead.",
		}),
		txRetries: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "db_tx_retries_total",
			Help: "Number of TxRetry retry attempts (the first attempt is not counted). Terminal outcomes still flow through db_tx_total{kind=tx,outcome=…}; this counter measures contention pressure.",
		}),
		replicaLag: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "db_replica_lag_seconds",
			Help: "Per-replica replication lag in seconds (from `now() - pg_last_xact_replay_timestamp()`); -1 when the most recent probe failed.",
		}, []string{"pool"}),
		replicaSkipped: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "db_replica_skipped_total",
			Help: "Number of times the read router skipped a replica because it was unhealthy (failed probe) or over the WithReadLagBudget threshold.",
		}, []string{"pool", "reason"}),
		replicaFallback: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "db_replica_fallback_total",
			Help: "Number of read queries that fell back to the primary because every configured replica was filtered out (unhealthy AND/OR over budget). A non-zero rate is the alert signal that replica health has degraded.",
		}),
	}
	reg.MustRegister(mc)
	return mc
}

// Describe implements prometheus.Collector.
func (m *metricsCollector) Describe(ch chan<- *prometheus.Desc) {
	m.poolSize.Describe(ch)
	m.duration.Describe(ch)
	m.txTotal.Describe(ch)
	m.txLatency.Describe(ch)
	m.slowQuery.Describe(ch)
	m.txRetries.Describe(ch)
	m.replicaLag.Describe(ch)
	m.replicaSkipped.Describe(ch)
	m.replicaFallback.Describe(ch)
}

// Collect implements prometheus.Collector. Refreshes pool gauges from the
// underlying pgxpool on every scrape, then delegates emission.
func (m *metricsCollector) Collect(ch chan<- prometheus.Metric) {
	m.refreshPoolStats()
	m.poolSize.Collect(ch)
	m.duration.Collect(ch)
	m.txTotal.Collect(ch)
	m.txLatency.Collect(ch)
	m.slowQuery.Collect(ch)
	m.txRetries.Collect(ch)
	m.replicaLag.Collect(ch)
	m.replicaSkipped.Collect(ch)
	m.replicaFallback.Collect(ch)
}

// incReplicaSkipped bumps the per-replica skip counter labelled by
// reason. nil-safe.
func (m *metricsCollector) incReplicaSkipped(pool, reason string) {
	if m == nil {
		return
	}
	m.replicaSkipped.WithLabelValues(pool, reason).Inc()
}

// incReplicaFallback bumps the all-replicas-down counter. nil-safe.
func (m *metricsCollector) incReplicaFallback() {
	if m == nil {
		return
	}
	m.replicaFallback.Inc()
}

// setReplicaLag updates the per-pool replica-lag gauge. No-op on nil
// receiver so the polling goroutine doesn't have to branch on "metrics
// enabled?" — it always calls through and the gauge fires only when
// the collector was actually built.
func (m *metricsCollector) setReplicaLag(name string, seconds float64) {
	if m == nil {
		return
	}
	m.replicaLag.WithLabelValues(name).Set(seconds)
}

func (m *metricsCollector) attach(name string, p *pgxpool.Pool) {
	m.pools[name] = p
}

func (m *metricsCollector) observe(name string, elapsed time.Duration, err error) {
	outcome := "success"
	if err != nil {
		outcome = "error"
	}
	m.duration.WithLabelValues(name, outcome).Observe(elapsed.Seconds())
}

// observeTx records the outcome of a top-level transaction or
// savepoint. kind is "tx" or "savepoint"; outcome is one of "commit",
// "rollback", "panic". elapsed is the wall time from BeginTx through
// Commit/Rollback completion.
func (m *metricsCollector) observeTx(kind, outcome string, elapsed time.Duration) {
	if m == nil {
		return
	}
	m.txTotal.WithLabelValues(kind, outcome).Inc()
	m.txLatency.WithLabelValues(kind, outcome).Observe(elapsed.Seconds())
}

// incSlowQuery records one slow-query event. Called by the tracer
// when elapsed > slowThreshold AND the query itself succeeded
// (errored queries already feed db_query_duration_seconds{outcome=error}).
func (m *metricsCollector) incSlowQuery() {
	if m == nil {
		return
	}
	m.slowQuery.Inc()
}

// incTxRetry records one TxRetry retry attempt. The first attempt of a
// TxRetry call is NOT counted; only retries after the initial failure.
func (m *metricsCollector) incTxRetry() {
	if m == nil {
		return
	}
	m.txRetries.Inc()
}

func (m *metricsCollector) setPoolStat(name string, s poolStat) {
	m.poolSize.WithLabelValues(name, "acquired").Set(float64(s.Acquired))
	m.poolSize.WithLabelValues(name, "idle").Set(float64(s.Idle))
	m.poolSize.WithLabelValues(name, "max").Set(float64(s.Max))
	m.poolSize.WithLabelValues(name, "total").Set(float64(s.Total))
}

func (m *metricsCollector) refreshPoolStats() {
	for name, p := range m.pools {
		s := p.Stat()
		m.setPoolStat(name, poolStat{
			Acquired: s.AcquiredConns(),
			Idle:     s.IdleConns(),
			Max:      s.MaxConns(),
			Total:    s.TotalConns(),
		})
	}
}
