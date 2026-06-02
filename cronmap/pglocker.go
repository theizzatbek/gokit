package cronmap

import (
	"context"

	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/db/lock"
)

// PGLocker is the kit-default [SingletonLocker] backed by Postgres
// advisory locks via [db/lock]. Pass to [WithSingletonLocker] when
// any job in your YAML has `singleton: true`:
//
//	rt, err := eng.Build(
//	    cronmap.WithSingletonLocker(cronmap.PGLocker(svc.DB)),
//	)
//
// Each TryLock call creates a fresh [lock.Lock] keyed by the job
// name, attempts pg_try_advisory_lock, and surfaces the result.
// Failure to acquire (lock held by another instance) is the
// EXPECTED path in N-1 of N pods; the locker returns (nil, false,
// nil) and the runtime increments cronmap_singleton_skipped_total.
//
// Returns nil when d is nil so callers can wire it unconditionally
// alongside an optional *db.DB.
func PGLocker(d *db.DB) SingletonLocker {
	if d == nil {
		return nil
	}
	return &pgLocker{d: d}
}

type pgLocker struct {
	d *db.DB
}

// TryLock acquires the pg_try_advisory_lock keyed by name. The
// returned release runs the [lock.ReleaseFunc] which releases the
// lock AND returns the dedicated conn to the pool.
func (p *pgLocker) TryLock(ctx context.Context, name string) (func(), bool, error) {
	lk := lock.New(p.d, name)
	acquired, releaseFn, err := lk.TryAcquire(ctx)
	if err != nil {
		return nil, false, err
	}
	if !acquired {
		return nil, false, nil
	}
	return func() { releaseFn() }, true, nil
}
