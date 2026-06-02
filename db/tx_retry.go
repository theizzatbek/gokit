package db

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/theizzatbek/gokit/errs"
)

// TxRetryOption configures TxRetry.
type TxRetryOption func(*txRetryOpts)

type txRetryOpts struct {
	maxAttempts int
	baseBackoff time.Duration
	maxBackoff  time.Duration
	isRetryable func(error) bool
	tx          TxOpts
}

// WithTxRetryOpts pairs each retry attempt with explicit TxOpts. The
// canonical "SERIALIZABLE + auto-retry" pattern reads:
//
//	db.TxRetry(ctx, fn, db.WithTxRetryOpts(db.TxOpts{IsoLevel: db.Serializable}))
func WithTxRetryOpts(t TxOpts) TxRetryOption {
	return func(o *txRetryOpts) { o.tx = t }
}

// WithTxRetryMaxAttempts caps the total number of Tx attempts. Must be > 0.
// Default 3 (one initial + up to two retries).
func WithTxRetryMaxAttempts(n int) TxRetryOption {
	return func(o *txRetryOpts) { o.maxAttempts = n }
}

// WithTxRetryBackoff sets the exponential-backoff base + ceiling between
// attempts. Default base 5ms, max 100ms — biased low because serialization
// retries usually resolve on the next attempt; long sleeps just stall the
// caller. Each wait carries ±25% jitter.
func WithTxRetryBackoff(base, max time.Duration) TxRetryOption {
	return func(o *txRetryOpts) { o.baseBackoff, o.maxBackoff = base, max }
}

// WithTxRetryClassifier overrides the default "retry on 40001/40P01"
// policy. Return true to keep retrying, false to surface the error
// immediately. Use to add app-specific retryable conditions
// (statement_timeout under load, unique-constraint races resolved by
// upsert-with-savepoint, …).
func WithTxRetryClassifier(fn func(error) bool) TxRetryOption {
	return func(o *txRetryOpts) { o.isRetryable = fn }
}

// IsRetryableTxConflict reports whether err is a Postgres serialization
// failure (SQLSTATE 40001) or deadlock (40P01) — both of which Postgres
// guarantees are safe to retry against the same data. The check walks
// the error chain via errors.As, so a *errs.Error wrapping the original
// pgconn.PgError still resolves correctly.
func IsRetryableTxConflict(err error) bool {
	if err == nil {
		return false
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		switch pg.Code {
		case "40001", "40P01":
			return true
		}
	}
	return false
}

// TxRetry opens a transaction, runs fn, and on serialization-failure or
// deadlock retries up to MaxAttempts times with exponential backoff +
// jitter. fn MUST be idempotent — it may run repeatedly against fresh
// transactions. Non-retryable errors surface immediately on the first
// attempt; ctx cancellation during a backoff sleep returns *errs.Error
// of KindUnavailable.
//
// Counter `db_tx_retries_total` increments once per retry attempt
// (NOT per failed Tx; the first attempt is not counted). The terminal
// outcome (commit / rollback / panic) is recorded under
// `db_tx_total{kind=tx}` exactly once, by the underlying Tx call that
// produced it.
//
// Default policy: MaxAttempts=3, BaseBackoff=5ms, MaxBackoff=100ms,
// retryable = IsRetryableTxConflict. Override via the WithTxRetry*
// options.
func (d *DB) TxRetry(ctx context.Context, fn func(*Tx) error, opts ...TxRetryOption) error {
	o := txRetryOpts{
		maxAttempts: 3,
		baseBackoff: 5 * time.Millisecond,
		maxBackoff:  100 * time.Millisecond,
		isRetryable: IsRetryableTxConflict,
	}
	for _, set := range opts {
		set(&o)
	}
	if o.maxAttempts < 1 {
		o.maxAttempts = 1
	}

	var lastErr error
	for attempt := 1; attempt <= o.maxAttempts; attempt++ {
		if attempt > 1 {
			wait := jitter(backoffWait(attempt-1, o.baseBackoff, o.maxBackoff))
			select {
			case <-ctx.Done():
				return errs.Wrap(ctx.Err(), errs.KindUnavailable, "db_unavailable", "TxRetry cancelled during backoff")
			case <-time.After(wait):
			}
			if d.opts.metrics != nil {
				d.opts.metrics.incTxRetry()
			}
		}
		err := d.TxWithOpts(ctx, o.tx, fn)
		if err == nil {
			return nil
		}
		if !o.isRetryable(err) {
			return err
		}
		lastErr = err
	}
	return lastErr
}

// jitter applies ±25% uniform noise to d. Defends against the
// "synchronized retry storm" where every contending caller waits
// exactly the same interval and re-collides. Negative / zero inputs
// pass through unchanged.
func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return d
	}
	delta := float64(d) * 0.25
	noise := (rand.Float64()*2 - 1) * delta
	return d + time.Duration(noise)
}
