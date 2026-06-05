package db

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/theizzatbek/gokit/errs"
)

// Batch is the kit's thin wrapper around pgx.Batch — collects N
// statements that ship to Postgres in one round-trip via the
// extended-query protocol. Created and driven by [SendBatch]; users
// only call [Batch.Queue] to append statements.
//
// The kit-side wrapper exists to:
//
//   - Apply `mapPgxErr` to every per-statement result, so each
//     statement's error surfaces as `*errs.Error` like a regular
//     `*DB.Exec` would.
//   - Track the queued-statement count so [SendBatch] can iterate the
//     results without the caller having to remember.
//
// Use for repository operations that naturally cluster — write to
// table A then update a counter in table B inside the same business
// transaction. For very large bulk inserts use [DB.CopyFrom] instead;
// batch's overhead is per-statement protocol framing.
type Batch struct {
	pgxBatch *pgx.Batch
	count    int
}

// Queue appends one statement to the batch. Returns the receiver to
// allow short fluent chains.
//
//	b := db.NewBatch().
//	    Queue(`UPDATE accounts SET balance = balance - $1 WHERE id = $2`, amt, from).
//	    Queue(`UPDATE accounts SET balance = balance + $1 WHERE id = $2`, amt, to)
func (b *Batch) Queue(sql string, args ...any) *Batch {
	b.pgxBatch.Queue(sql, args...)
	b.count++
	return b
}

// Len reports the number of queued statements. Useful for asserts in
// callers that assemble batches conditionally.
func (b *Batch) Len() int { return b.count }

// NewBatch returns an empty *Batch ready for Queue calls. Send it to
// the server via [DB.SendBatch] or [Tx.SendBatch].
func NewBatch() *Batch {
	return &Batch{pgxBatch: &pgx.Batch{}}
}

// SendBatch ships the batched statements to Postgres in one round-trip
// and returns a [BatchResults] iterator over the per-statement
// outcomes. Caller MUST call [BatchResults.Close] when done — the
// underlying pgx connection stays held until then.
//
// Errors from network / protocol problems return non-nil err with a
// nil BatchResults. Errors from individual statements surface from
// [BatchResults.Exec] / [BatchResults.Query] / [BatchResults.QueryRow]
// on iteration.
func (d *DB) SendBatch(ctx context.Context, b *Batch) (*BatchResults, error) {
	if d == nil || d.pool == nil {
		return nil, errs.Unavailable("db_unavailable", "db pool is closed")
	}
	if b == nil || b.count == 0 {
		return nil, errs.Validation("db_empty_batch", "db.SendBatch: batch is empty")
	}
	br := d.pool.SendBatch(ctx, b.pgxBatch)
	return &BatchResults{inner: br, remaining: b.count}, nil
}

// SendBatch on *Tx ships the batched statements inside the open
// transaction. Same semantics as [DB.SendBatch].
func (t *Tx) SendBatch(ctx context.Context, b *Batch) (*BatchResults, error) {
	if t == nil || t.tx == nil {
		return nil, errs.Internal("db_tx_closed", "db.SendBatch: tx is closed")
	}
	if b == nil || b.count == 0 {
		return nil, errs.Validation("db_empty_batch", "db.SendBatch: batch is empty")
	}
	br := t.tx.SendBatch(ctx, b.pgxBatch)
	return &BatchResults{inner: br, remaining: b.count}, nil
}

// BatchResults iterates the per-statement outcomes from a [SendBatch]
// call. Call [BatchResults.Exec] / [BatchResults.Query] /
// [BatchResults.QueryRow] in QUEUE ORDER to advance through the
// results; the iterator does not allow skipping or random access (pgx
// pipelines statements in submission order and the protocol replies
// the same way).
type BatchResults struct {
	inner     pgx.BatchResults
	remaining int
	closed    bool
}

// Exec advances to the next batch result and returns its
// RowsAffected. Returns an error AND advances when the underlying
// statement failed. Calling Exec / Query / QueryRow more times than
// were Queued returns a stable Code so test failures point at the
// bug.
func (r *BatchResults) Exec() (int64, error) {
	if err := r.advance(); err != nil {
		return 0, err
	}
	tag, err := r.inner.Exec()
	return tag.RowsAffected(), mapPgxErr(err)
}

// Query advances to the next batch result and returns the pgx.Rows.
// Caller MUST call rows.Close before invoking the next
// Exec/Query/QueryRow on the same BatchResults.
func (r *BatchResults) Query() (pgx.Rows, error) {
	if err := r.advance(); err != nil {
		return nil, err
	}
	rows, err := r.inner.Query()
	return rows, mapPgxErr(err)
}

// QueryRow advances to the next batch result and returns a single
// row. The row's Scan error is funneled through `mapPgxErr` for
// consistency with `*DB.QueryRow`.
func (r *BatchResults) QueryRow() pgx.Row {
	if err := r.advance(); err != nil {
		return errorRow{err: err}
	}
	return mappedRow{inner: r.inner.QueryRow()}
}

// Close releases the underlying pgx connection back to the pool. Safe
// + idempotent. Always defer this immediately after a successful
// SendBatch — leaking a BatchResults silently holds a connection for
// the rest of the process lifetime.
func (r *BatchResults) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	return mapPgxErr(r.inner.Close())
}

// advance consumes one slot from the remaining counter and refuses
// over-iteration with a stable Code.
func (r *BatchResults) advance() error {
	if r.remaining <= 0 {
		return errs.Internal("db_batch_overrun", "db: BatchResults read past last queued statement")
	}
	r.remaining--
	return nil
}

// errorRow lets QueryRow return a Row whose Scan replays the
// advance() error. Mirrors pgx's own "row carries error" pattern.
type errorRow struct{ err error }

func (r errorRow) Scan(_ ...any) error { return r.err }
