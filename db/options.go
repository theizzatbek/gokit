package db

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"
)

// Option configures Connect.
type Option func(*options)

// ConnInitFn runs once per fresh pgx connection BEFORE the connection
// joins the pool. Use to set session-level state (statement_timeout,
// search_path, application_name, role) or to warm any prepared-stmt
// caches the kit doesn't manage. A non-nil return causes pgx to
// discard the connection and re-dial.
type ConnInitFn func(ctx context.Context, conn *pgx.Conn) error

type options struct {
	logger           *slog.Logger
	slowThreshold    time.Duration
	metrics          *metricsCollector
	extraTracers     []pgx.QueryTracer
	statementTimeout time.Duration
	connInit         []ConnInitFn
	lagPoll          lagPollConfig
}

// lagPollConfig captures the WithReplicaLagPolling settings.
type lagPollConfig struct {
	interval  time.Duration
	threshold time.Duration
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

// WithDefaultStatementTimeout sets the server-side `statement_timeout`
// (in milliseconds) for every connection the pool opens. Applied via
// an AfterConnect hook with a single `SET statement_timeout = <ms>`.
// 0 = no timeout (Postgres default).
//
// Use as a defence-in-depth against runaway queries: a single bad
// query can otherwise hold a pool connection indefinitely, and the
// caller's context.WithTimeout only kills the local goroutine — the
// server keeps churning until the query completes. statement_timeout
// kills the query on the server too.
//
// Per-query overrides via context.WithTimeout still take precedence
// (pgx's CancelRequest path fires before statement_timeout in
// practice). Override per-statement with `SET LOCAL statement_timeout`
// inside a Tx.
func WithDefaultStatementTimeout(d time.Duration) Option {
	return func(o *options) { o.statementTimeout = d }
}

// WithConnInit registers a hook called once per fresh pgx connection
// BEFORE the connection joins the pool. Multiple WithConnInit calls
// accumulate in registration order; the kit-internal statement_timeout
// hook (when WithDefaultStatementTimeout is set) runs first.
//
// Typical uses: `SET application_name = '…/replica1'`, `SET search_path
// = app, public`, warming a prepared-statement cache, switching role
// via SET ROLE for tenant isolation. A non-nil return causes pgx to
// discard the connection and re-dial.
func WithConnInit(fn ConnInitFn) Option {
	return func(o *options) {
		if fn != nil {
			o.connInit = append(o.connInit, fn)
		}
	}
}

// WithReplicaLagPolling spawns a background goroutine that polls every
// configured read-replica's replication lag at `interval` and updates
// the `db_replica_lag_seconds{pool}` gauge (requires [WithMetrics] for
// the gauge to be registered — without it the polling still runs and
// emits WARN logs, just no Prometheus surface).
//
// When `threshold > 0`, a per-replica lag above threshold emits a
// structured WARN via [WithLogger] (silent without a logger). Set
// threshold = 0 to disable the warning entirely while still feeding the
// gauge.
//
// `interval ≤ 0` disables the goroutine (the kit also disables it when
// no read-replica is configured — option is a no-op in that case). The
// goroutine exits on [DB.Close].
//
// Lag is read via `SELECT EXTRACT(EPOCH FROM (now() -
// pg_last_xact_replay_timestamp()))::float8` per replica. Primary
// nodes (e.g. when an operator points DB_READ_URLS at a writable
// instance) return NULL → kit reports lag=0 + Healthy=true so the
// gauge doesn't appear as "infinite lag".
func WithReplicaLagPolling(interval, threshold time.Duration) Option {
	return func(o *options) {
		o.lagPoll.interval = interval
		o.lagPoll.threshold = threshold
	}
}

// WithTracer attaches an external pgx.QueryTracer that runs alongside
// the kit's internal logger/metrics tracer. Use to plug OpenTelemetry
// (via [otelkit.NewPgxTracer]) or any other tracer that follows pgx's
// QueryTracer contract — TraceQueryStart returns a derived context;
// TraceQueryEnd reads it. The kit composes multiple tracers
// internally so calling WithTracer more than once stacks them in
// registration order, and the kit's own tracer always fires first.
//
// service.WithOtel auto-applies an OTel pgx tracer when both DB and
// OTel are configured — callers usually never need this option
// directly. Reach for it when wiring a non-OTel tracing backend
// (Datadog tracing, custom audit trail, etc.).
func WithTracer(t pgx.QueryTracer) Option {
	return func(o *options) {
		if t != nil {
			o.extraTracers = append(o.extraTracers, t)
		}
	}
}
