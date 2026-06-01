// Package lock is the kit's Postgres advisory-lock primitive.
//
// Built around `pg_try_advisory_lock` (non-blocking) and
// `pg_advisory_lock` (blocking) on a dedicated pool connection.
// Names hash to int64 via the first 8 bytes of sha256, so identical
// `name` strings always map to the same lock key across replicas.
//
// Lifecycle:
//
//	lk := lock.New(svc.DB, "daily-rollup")
//
//	acquired, release, err := lk.TryAcquire(ctx)
//	if err != nil { return err }
//	if !acquired { return nil /* another replica holds it */ }
//	defer release()
//
//	// critical section
//
// Convenience wrappers fold the acquire/release dance into one call:
//
//	if err := lock.RunOnce(ctx, svc.DB, "daily-rollup", fn); err != nil {
//	    return err
//	}
//
// # Used internally by
//
// service.WithSingletonCron wraps the supplied JobFn with a
// `TryAcquire + run + release` block keyed by the job name — only
// ONE replica per multi-replica deployment runs the job per tick.
// Exposing the primitive lets app code do the same for any race-
// sensitive one-shot.
package lock

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/errs"
)

// Stable error Code constants for lock operations.
const (
	// CodeAcquireFailed — pg_advisory_lock / pg_try_advisory_lock
	// errored for non-cancel reasons (conn drop, server down).
	CodeAcquireFailed = "lock_acquire_failed"

	// CodeReleaseFailed — pg_advisory_unlock errored. Session-level
	// locks auto-release when the conn returns to the pool, so this
	// is usually log-only.
	CodeReleaseFailed = "lock_release_failed"

	// CodeNilDB — Lock constructed with nil *db.DB.
	CodeNilDB = "lock_nil_db"

	// CodeEmptyName — Lock constructed with empty name. The key is
	// derived from the name, so empty would collide with every
	// other empty-name lock.
	CodeEmptyName = "lock_empty_name"
)

// Lock identifies an advisory lock by name + the underlying pool.
// Built once at startup; thread-safe — TryAcquire / Acquire may be
// called from multiple goroutines, each gets its own conn from the
// pool.
type Lock struct {
	db   *db.DB
	name string
	key  int64
}

// New constructs a Lock. Panics on nil d or empty name — both are
// programmer errors caught at startup. The key derives from
// sha256(name)[:8] interpreted as big-endian int64, so two
// different services using the same name will fight over the same
// lock. Namespace per-service via prefix: "orders.daily-rollup".
func New(d *db.DB, name string) *Lock {
	if d == nil {
		panic(errs.Validation(CodeNilDB, "lock: nil *db.DB"))
	}
	if name == "" {
		panic(errs.Validation(CodeEmptyName, "lock: empty name"))
	}
	return &Lock{db: d, name: name, key: keyOf(name)}
}

// Name returns the human-readable lock name.
func (l *Lock) Name() string { return l.name }

// Key returns the int64 advisory-lock key derived from the name.
// Useful for ops queries like
// `SELECT pid FROM pg_locks WHERE objid = $1`.
func (l *Lock) Key() int64 { return l.key }

// ReleaseFunc releases the underlying conn back to the pool AND
// calls `pg_advisory_unlock`. Idempotent — calling twice is a
// no-op. Session-level lock semantics: even if Release is never
// called, the lock auto-releases when the pool closes the conn,
// so a panicking handler doesn't leak the lock forever.
type ReleaseFunc func()

// TryAcquire attempts pg_try_advisory_lock on a freshly-acquired
// pool conn. Returns (acquired=true, release, nil) when the lock
// is now held — caller MUST call release in a defer. Returns
// (false, nil, nil) when another holder has it; this is the
// expected "skip" path, not an error.
//
// Acquire errors from the underlying pool / pgx surface as
// *errs.Error{Code: CodeAcquireFailed}.
func (l *Lock) TryAcquire(ctx context.Context) (bool, ReleaseFunc, error) {
	conn, err := l.db.Pool().Acquire(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return false, nil, err
		}
		return false, nil, errs.Wrap(err, errs.KindUnavailable, CodeAcquireFailed,
			"lock: acquire conn")
	}
	var ok bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", l.key).Scan(&ok); err != nil {
		conn.Release()
		return false, nil, errs.Wrap(err, errs.KindUnavailable, CodeAcquireFailed,
			"lock: pg_try_advisory_lock")
	}
	if !ok {
		conn.Release()
		return false, nil, nil
	}
	return true, makeRelease(conn, l.key), nil
}

// Acquire blocks until pg_advisory_lock succeeds OR ctx is
// cancelled. Use when the critical section MUST run on this
// replica (vs. TryAcquire's "run if I'm the winner" semantics).
//
// Returns the release func on success; ctx errors propagate.
// Other underlying errors map to *errs.Error{Code: CodeAcquireFailed}.
func (l *Lock) Acquire(ctx context.Context) (ReleaseFunc, error) {
	conn, err := l.db.Pool().Acquire(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, errs.Wrap(err, errs.KindUnavailable, CodeAcquireFailed,
			"lock: acquire conn")
	}
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", l.key); err != nil {
		conn.Release()
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, errs.Wrap(err, errs.KindUnavailable, CodeAcquireFailed,
			"lock: pg_advisory_lock")
	}
	return makeRelease(conn, l.key), nil
}

// RunOnce is the convenience wrapper around TryAcquire: if the
// lock is free, run fn and release on return; otherwise skip
// silently and return nil. Errors from fn propagate unchanged so
// the caller's error-handling stays straightforward.
//
//	if err := lock.RunOnce(ctx, svc.DB, "daily-rollup", func(ctx context.Context) error {
//	    return rollup.Run(ctx)
//	}); err != nil { return err }
func RunOnce(ctx context.Context, d *db.DB, name string, fn func(context.Context) error) error {
	lk := New(d, name)
	ok, release, err := lk.TryAcquire(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	defer release()
	return fn(ctx)
}

// RunBlocking is RunOnce's blocking sibling: waits for the lock
// (via Acquire), runs fn, releases. Ctx cancellation aborts the
// wait.
func RunBlocking(ctx context.Context, d *db.DB, name string, fn func(context.Context) error) error {
	lk := New(d, name)
	release, err := lk.Acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	return fn(ctx)
}

// keyOf derives the int64 advisory-lock key from a name. First 8
// bytes of sha256, big-endian, signed cast — matches Postgres's
// `bigint` lock-key signature.
func keyOf(name string) int64 {
	sum := sha256.Sum256([]byte(name))
	return int64(binary.BigEndian.Uint64(sum[:8]))
}

// makeRelease builds a ReleaseFunc that runs once: unlocks +
// releases the conn. Subsequent calls are no-ops.
func makeRelease(conn *pgxpool.Conn, key int64) ReleaseFunc {
	released := false
	return func() {
		if released {
			return
		}
		released = true
		// Best-effort unlock — session-level lock auto-releases on
		// conn close anyway, so an unlock failure here just slightly
		// delays the cleanup until the conn is reaped by the pool.
		// Background ctx because the caller's ctx may already be
		// cancelled at defer time.
		_, _ = conn.Exec(context.Background(),
			"SELECT pg_advisory_unlock($1)", key)
		conn.Release()
	}
}
