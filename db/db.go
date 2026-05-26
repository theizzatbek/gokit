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
type DB struct {
	pool *pgxpool.Pool
	opts options
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
func Connect(ctx context.Context, cfg Config, opts ...Option) (*DB, error) {
	raw, err := buildPgxURL(cfg, "read-write")
	if err != nil {
		return nil, errs.Wrap(err, errs.KindInternal, "db_config_invalid", "could not build db url")
	}
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

	o := options{}
	for _, fn := range opts {
		fn(&o)
	}
	if o.logger != nil || o.metrics != nil {
		if o.slowThreshold == 0 {
			o.slowThreshold = 500 * time.Millisecond
		}
		pgxCfg.ConnConfig.Tracer = &tracer{
			logger:        o.logger,
			metrics:       o.metrics,
			slowThreshold: o.slowThreshold,
		}
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
		o.metrics.attach(pool)
	}
	return &DB{pool: pool, opts: o}, nil
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

// Close releases the underlying pool. Safe to call multiple times.
func (d *DB) Close() {
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

var _ Querier = (*DB)(nil)
