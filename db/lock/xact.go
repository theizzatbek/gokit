package lock

import (
	"context"

	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/errs"
)

// TryAcquireXact attempts `pg_try_advisory_xact_lock` inside tx.
// Unlike [Lock.TryAcquire], no manual release is required — the
// lock is automatically released on tx commit OR rollback.
//
// Use when the critical section is already wrapped in a [*db.Tx]:
//
//	err := svc.DB.Tx(ctx, func(tx *db.Tx) error {
//	    lk := lock.New(svc.DB, "orders.batch-charge")
//	    ok, err := lk.TryAcquireXact(ctx, tx)
//	    if err != nil { return err }
//	    if !ok { return nil /* another tx holds it */ }
//
//	    // ... critical section: read + UPDATE rows ...
//	    return nil
//	})
//
// Returns (true, nil) when the lock is now held by this transaction;
// (false, nil) when another backend holds it. Errors from the
// underlying query surface as *errs.Error{Code: CodeAcquireFailed}.
//
// Locks acquired this way:
//   - share the namespace with session-level [Lock.TryAcquire] —
//     mixing the two on the same name interlocks correctly.
//   - record an `acquired` / `contended` metric outcome the same way
//     as TryAcquire, but observeHold is NOT called (the release point
//     is implicit, owned by Postgres tx end).
//
// tx MUST be the *db.Tx the surrounding fn received; passing a tx
// from a different transaction defeats the auto-release.
func (l *Lock) TryAcquireXact(ctx context.Context, tx *db.Tx) (bool, error) {
	if tx == nil {
		l.metrics.recordOutcome(outcomeError)
		return false, errs.Validation(CodeAcquireFailed,
			"lock: TryAcquireXact: nil *db.Tx")
	}
	var ok bool
	if err := tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock($1)", l.key).Scan(&ok); err != nil {
		l.metrics.recordOutcome(outcomeError)
		l.logAcquireErr(err)
		return false, errs.Wrap(err, errs.KindUnavailable, CodeAcquireFailed,
			"lock: pg_try_advisory_xact_lock")
	}
	if !ok {
		l.metrics.recordOutcome(outcomeContended)
		l.logContended()
		return false, nil
	}
	l.metrics.recordOutcome(outcomeAcquired)
	l.logAcquired()
	return true, nil
}
