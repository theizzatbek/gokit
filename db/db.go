package db

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/theizzatbek/fibermap/errs"
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
func Connect(ctx context.Context, cfg Config, opts ...Option) (*DB, error) {
	pgxCfg, err := pgxpool.ParseConfig(buildConnString(cfg))
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
	// Options that configure pgx (e.g. tracer) are applied in Task 8.
	// For now the slot is reserved.

	pool, err := pgxpool.NewWithConfig(ctx, pgxCfg)
	if err != nil {
		return nil, errs.Wrap(err, errs.KindUnavailable, "db_unavailable", "could not open db pool")
	}

	pingCtx := ctx
	if cfg.ConnectTimeout > 0 {
		var cancel context.CancelFunc
		pingCtx, cancel = context.WithTimeout(ctx, cfg.ConnectTimeout)
		defer cancel()
	}
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, errs.Wrap(err, errs.KindUnavailable, "db_unavailable", "could not reach db")
	}
	return &DB{pool: pool, opts: o}, nil
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
