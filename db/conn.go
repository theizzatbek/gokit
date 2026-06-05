package db

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/theizzatbek/gokit/errs"
)

// WithConn acquires one connection from the primary pool, runs fn
// with it pinned to that conn, and releases the conn back on return.
// Use when a sequence of queries MUST land on the same physical
// connection — temp tables (session-scoped), prepared statements
// reused across multiple Exec/Query calls, cursor FETCH, or
// `SET LOCAL` semantics outside a transaction.
//
// The supplied conn is a *pgxpool.Conn — call Conn.Query/Exec/etc. on
// it directly. Errors from fn surface verbatim (no `mapPgxErr`
// wrapping — the caller picks the surface they want); the
// Acquire/Release failure path is mapped to *errs.Error
// `KindUnavailable` with stable Code `db_unavailable`.
//
// Nested WithConn calls are NOT special — each acquires a fresh conn.
// For ergonomic in-transaction conn pinning, use [Tx.WithConn].
//
//	err := svc.DB.WithConn(ctx, func(conn *pgxpool.Conn) error {
//	    if _, err := conn.Exec(ctx, `CREATE TEMP TABLE stage (id int)`); err != nil {
//	        return err
//	    }
//	    if _, err := conn.Exec(ctx, `INSERT INTO stage SELECT generate_series(1, 1000)`); err != nil {
//	        return err
//	    }
//	    _, err := conn.Exec(ctx, `INSERT INTO users (id) SELECT id FROM stage`)
//	    return err
//	    // stage drops automatically when conn returns to the pool.
//	})
func (d *DB) WithConn(ctx context.Context, fn func(*pgxpool.Conn) error) error {
	if d == nil || d.pool == nil {
		return errs.Unavailable("db_unavailable", "db pool is closed")
	}
	if fn == nil {
		return errs.Validation("db_nil_fn", "db.WithConn: fn is nil")
	}
	conn, err := d.pool.Acquire(ctx)
	if err != nil {
		return errs.Wrap(err, errs.KindUnavailable, "db_unavailable", "db.WithConn: acquire")
	}
	defer conn.Release()
	return fn(conn)
}

// WithReadConn is the read-replica variant of [WithConn]. Picks a
// read pool via the standard routing (round-robin / random / lag
// budget) and pins the supplied fn to one conn from it. Falls back to
// the primary pool when no read pool is configured or every replica
// is filtered out — same fallback policy as ReadQuery.
//
// Wrap ctx with [ReadFromPrimary] to force a primary conn.
func (d *DB) WithReadConn(ctx context.Context, fn func(*pgxpool.Conn) error) error {
	if d == nil || d.pool == nil {
		return errs.Unavailable("db_unavailable", "db pool is closed")
	}
	if fn == nil {
		return errs.Validation("db_nil_fn", "db.WithReadConn: fn is nil")
	}
	pool := d.pickReadPool(ctx)
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return errs.Wrap(err, errs.KindUnavailable, "db_unavailable", "db.WithReadConn: acquire")
	}
	defer conn.Release()
	return fn(conn)
}
