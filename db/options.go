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
//
// The pool label distinguishes the primary write pool from the read-replica
// pool opened when cfg.HasReadReplica is true. With a single pool only the
// pool="primary" series is emitted.
//
// Without this option, no collectors are created (zero Prometheus footprint).
func WithMetrics(reg prometheus.Registerer) Option {
	return func(o *options) { o.metrics = newMetricsCollector(reg) }
}
