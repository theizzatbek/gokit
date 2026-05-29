package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Tx is the in-transaction handle. It implements Querier so the same
// repository code works against *DB and *Tx. Nested Tx calls open a
// savepoint via pgx.Tx.Begin (see Task 7).
type Tx struct {
	tx      pgx.Tx
	metrics *metricsCollector // nil when WithMetrics wasn't wired; inherited by nested savepoints
}

// Tx opens a transaction, runs fn, and commits on nil return or rolls back
// on non-nil/panic. The handle MUST NOT escape fn — pgx invalidates it on
// commit/rollback.
func (d *DB) Tx(ctx context.Context, fn func(*Tx) error) error {
	tx, err := d.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return mapPgxErr(err)
	}
	return runInTx(ctx, tx, fn, d.opts.metrics, "tx")
}

func runInTx(ctx context.Context, tx pgx.Tx, fn func(*Tx) error, mc *metricsCollector, kind string) (err error) {
	start := time.Now()
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(ctx)
			mc.observeTx(kind, "panic", time.Since(start))
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback(ctx)
			mc.observeTx(kind, "rollback", time.Since(start))
			return
		}
		if cerr := tx.Commit(ctx); cerr != nil {
			err = mapPgxErr(cerr)
			mc.observeTx(kind, "rollback", time.Since(start))
			return
		}
		mc.observeTx(kind, "commit", time.Since(start))
	}()
	return fn(&Tx{tx: tx, metrics: mc})
}

func (t *Tx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return doQuery(ctx, t.tx, sql, args...)
}

func (t *Tx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return doQueryRow(ctx, t.tx, sql, args...)
}

func (t *Tx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return doExec(ctx, t.tx, sql, args...)
}

// Tx opens a savepoint inside the current transaction, runs fn, and releases
// the savepoint on nil return / rolls back to it on non-nil. Nestable.
func (t *Tx) Tx(ctx context.Context, fn func(*Tx) error) error {
	inner, err := t.tx.Begin(ctx) // pgx implements this as SAVEPOINT
	if err != nil {
		return mapPgxErr(err)
	}
	return runInTx(ctx, inner, fn, t.metrics, "savepoint")
}

var _ Querier = (*Tx)(nil)
