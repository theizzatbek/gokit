package db

import (
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Option configures Connect.
type Option func(*options)

type options struct {
	logger        *slog.Logger
	slowThreshold time.Duration
	metrics       *metricsCollector
}

// WithLogger wires a slog.Logger into the pgx QueryTracer. Without this
// option, queries are logged nowhere.
//
// Levels emitted by the tracer:
//   - Debug: every successful query (sql + elapsed). Off in prod by default.
//   - Warn:  queries slower than the slow-query threshold (see WithSlowQueryThreshold).
//   - Error: queries that returned an error.
func WithLogger(logger *slog.Logger) Option {
	return func(o *options) { o.logger = logger }
}

// WithSlowQueryThreshold sets the threshold above which queries are logged
// at Warn level (only effective alongside WithLogger). Default: 500ms.
func WithSlowQueryThreshold(d time.Duration) Option {
	return func(o *options) { o.slowThreshold = d }
}

// WithMetrics registers Prometheus collectors on reg:
//   - db_pool_size_total{pool="primary|standby", state="acquired|idle|max|total"} (gauge)
//   - db_query_duration_seconds{outcome="success|error"}                          (histogram)
//   - db_tx_total{kind="tx|savepoint", outcome="commit|rollback|panic"}           (counter)
//   - db_tx_duration_seconds{kind, outcome}                                       (histogram)
//   - db_slow_query_total                                                         (counter; populated when WithSlowQueryThreshold > 0)
//
// The pool label distinguishes the primary write pool from the read-replica
// pool opened when cfg.HasReadReplica is true. With a single pool only the
// pool="primary" series is emitted.
//
// kind="tx" is a top-level BEGIN; kind="savepoint" is a nested DB.Tx.Tx
// (pgx implements it as SAVEPOINT). outcome="rollback" covers both an
// explicit non-nil return from fn and a pgx Commit failure; "panic" is
// recorded when a panic propagates out of fn — the panic is re-thrown
// after rollback so the metric reflects what callers observe externally.
//
// db_slow_query_total counts queries whose execution exceeded the
// configured slow-query threshold AND completed successfully. Errored
// queries already feed db_query_duration_seconds{outcome=error}; they
// do not double-count here.
//
// Without this option, no collectors are created (zero Prometheus footprint).
func WithMetrics(reg prometheus.Registerer) Option {
	return func(o *options) { o.metrics = newMetricsCollector(reg) }
}
