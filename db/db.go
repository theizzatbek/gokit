package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/theizzatbek/gokit/errs"
)

// Querier is implemented by both *DB and *Tx so repository functions can be
// written once and called against either.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// DB wraps a *pgxpool.Pool with the kit's error-mapping and transaction helpers.
// When cfg.HasReadReplica was true at Connect time, DB also holds a second
// pool against target_session_attrs=standby exposed via ReadQuery/ReadQueryRow/ReadPool.
type DB struct {
	pool     *pgxpool.Pool // primary (target_session_attrs=read-write)
	readPool *pgxpool.Pool // standby (target_session_attrs=standby); nil when HasReadReplica=false
	opts     options
}

// Connect opens a connection pool with cfg + opts. The returned *DB owns the
// underlying *pgxpool.Pool; call Close to release it. Returns *errs.Error of
// KindUnavailable if the pool fails its initial sanity ping.
//
// When cfg.ConnectMaxRetries > 0, transient pool-create / ping failures are
// retried with exponential backoff (base = cfg.ConnectBackoffBase, doubling
// each attempt, capped at cfg.ConnectBackoffMax). The loop honours ctx.Done()
// during backoff sleeps, returning KindUnavailable with the ctx error. Default
// 0 = single attempt, preserving fail-fast behaviour for kit-direct callers.
//
// When cfg.HasReadReplica is true, a second pool is opened against the same
// connection string with target_session_attrs=standby. If the standby pool
// fails to connect, the primary pool is closed and an *errs.Error of
// KindUnavailable is returned — no silent degradation. With WithMetrics, the
// db_pool_size_total gauge gains the pool="primary|standby" label so each
// pool is observable independently.
func Connect(ctx context.Context, cfg Config, opts ...Option) (*DB, error) {
	o := options{}
	for _, fn := range opts {
		fn(&o)
	}
	if o.logger != nil || o.metrics != nil {
		if o.slowThreshold == 0 {
			o.slowThreshold = 500 * time.Millisecond
		}
	}

	primaryURL, err := buildPgxURL(cfg, "read-write")
	if err != nil {
		return nil, errs.Wrap(err, errs.KindInternal, "db_config_invalid", "could not build db url")
	}
	primary, err := connectPool(ctx, primaryURL, "primary", cfg, &o)
	if err != nil {
		return nil, err
	}

	d := &DB{pool: primary, opts: o}

	if cfg.HasReadReplica {
		readURL, err := buildPgxURL(cfg, "standby")
		if err != nil {
			primary.Close()
			return nil, errs.Wrap(err, errs.KindInternal, "db_config_invalid", "could not build read replica url")
		}
		readPool, err := connectPool(ctx, readURL, "standby", cfg, &o)
		if err != nil {
			primary.Close()
			return nil, err
		}
		d.readPool = readPool
	}

	return d, nil
}

// connectPool opens one pool against raw, applies cfg knobs and the tracer
// from o, and runs the retry loop. name ("primary" or "standby") is used as
// the pool label when attaching to metrics. Returns
// *errs.Error{Kind:KindUnavailable} on exhausted budget or ctx cancellation
// during backoff.
func connectPool(ctx context.Context, raw, name string, cfg Config, o *options) (*pgxpool.Pool, error) {
	pgxCfg, err := pgxpool.ParseConfig(raw)
	if err != nil {
		return nil, errs.Wrap(err, errs.KindInternal, "db_config_invalid", "could not parse db config")
	}
	if cfg.MaxConns > 0 {
		pgxCfg.MaxConns = cfg.MaxConns
	}
	if cfg.MinConns > 0 {
		pgxCfg.MinConns = cfg.MinConns
	}
	if cfg.MaxConnLifetime > 0 {
		pgxCfg.MaxConnLifetime = cfg.MaxConnLifetime
	}
	if cfg.MaxConnIdle > 0 {
		pgxCfg.MaxConnIdleTime = cfg.MaxConnIdle
	}
	if t := composeTracer(o); t != nil {
		pgxCfg.ConnConfig.Tracer = t
	}

	var pool *pgxpool.Pool
	for attempt := 0; attempt <= cfg.ConnectMaxRetries; attempt++ {
		if attempt > 0 {
			wait := backoffWait(attempt, cfg.ConnectBackoffBase, cfg.ConnectBackoffMax)
			if o.logger != nil {
				o.logger.Warn("db: connect failed, retrying",
					"attempt", attempt,
					"max_retries", cfg.ConnectMaxRetries,
					"wait", wait,
					"err", err)
			}
			select {
			case <-ctx.Done():
				return nil, errs.Wrap(ctx.Err(), errs.KindUnavailable, "db_unavailable", "connect cancelled")
			case <-time.After(wait):
			}
		}
		pool, err = pgxpool.NewWithConfig(ctx, pgxCfg)
		if err != nil {
			continue
		}
		pingCtx := ctx
		if cfg.ConnectTimeout > 0 {
			var cancel context.CancelFunc
			pingCtx, cancel = context.WithTimeout(ctx, cfg.ConnectTimeout)
			err = pool.Ping(pingCtx)
			cancel()
		} else {
			err = pool.Ping(pingCtx)
		}
		if err != nil {
			pool.Close()
			pool = nil
			continue
		}
		break
	}
	if err != nil {
		return nil, errs.Wrap(err, errs.KindUnavailable, "db_unavailable", "could not reach db")
	}

	if o.metrics != nil {
		o.metrics.attach(name, pool)
	}
	return pool, nil
}

// backoffWait returns the wait duration before attempt N (1-indexed).
// Exponential: base << (N-1), capped at max. Returns 0 if base <= 0.
func backoffWait(attempt int, base, max time.Duration) time.Duration {
	if base <= 0 {
		return 0
	}
	w := base << (attempt - 1)
	if w <= 0 || w > max {
		return max
	}
	return w
}

// Close releases the primary pool and the read pool when present.
// Safe to call multiple times.
func (d *DB) Close() {
	if d.readPool != nil {
		d.readPool.Close()
		d.readPool = nil
	}
	if d.pool == nil {
		return
	}
	d.pool.Close()
	d.pool = nil
}

// Pool returns the underlying *pgxpool.Pool for advanced use (LISTEN/NOTIFY,
// COPY, custom isolation). Errors via this path are NOT funneled through
// mapPgxErr — the caller owns mapping.
func (d *DB) Pool() *pgxpool.Pool { return d.pool }

// Query executes sql and returns the rows. The error is funneled through mapPgxErr.
func (d *DB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return doQuery(ctx, d.pool, sql, args...)
}

// QueryRow executes sql and returns a single row. The row's Scan error is
// funneled through mapPgxErr.
func (d *DB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return doQueryRow(ctx, d.pool, sql, args...)
}

// Exec executes sql. The error is funneled through mapPgxErr.
func (d *DB) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return doExec(ctx, d.pool, sql, args...)
}

// ReadQuery runs sql against the read-replica pool when HasReadReplica was
// true at Connect time; otherwise falls back to the primary pool. Use for
// SELECTs that tolerate replica lag — listings, search, analytics, plain
// GETs. NEVER use for SELECT FOR UPDATE or queries that must see
// just-written data; use Query for those.
func (d *DB) ReadQuery(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	pool := d.readPool
	if pool == nil {
		pool = d.pool
	}
	return doQuery(ctx, pool, sql, args...)
}

// ReadQueryRow is the single-row companion to ReadQuery; same semantics.
func (d *DB) ReadQueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	pool := d.readPool
	if pool == nil {
		pool = d.pool
	}
	return doQueryRow(ctx, pool, sql, args...)
}

// ReadPool returns the underlying standby *pgxpool.Pool when HasReadReplica
// was true at Connect time; nil otherwise. Use only for LISTEN/NOTIFY, COPY,
// or custom isolation — most code wants ReadQuery / ReadQueryRow.
func (d *DB) ReadPool() *pgxpool.Pool { return d.readPool }

var _ Querier = (*DB)(nil)
