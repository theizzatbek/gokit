package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Drain waits for every in-flight query / transaction to release
// its pool connection, then calls Close. Use during graceful
// shutdown to ensure handlers finish writing before the process
// exits.
//
//  1. Polls Pool().Stat().AcquiredConns() every 100ms until it
//     reaches 0 OR ctx is done.
//  2. Calls Close() unconditionally — even on ctx expiration —
//     so the pool is released regardless.
//  3. Returns ctx.Err() when the wait timed out, nil when the
//     drain completed cleanly.
//
// Idempotent + nil-safe: subsequent Drain / Close calls no-op.
//
//	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
//	defer cancel()
//	_ = svc.DB.Drain(ctx)
//
// service.Close uses Drain with a deadline derived from
// service.WithDBDrainTimeout (default 5s) so kit-based services
// drain cleanly without callers wiring anything.
func (d *DB) Drain(ctx context.Context) error {
	if d == nil || d.pool == nil {
		return nil
	}
	// Read-replica pools drain alongside the primary so a long-
	// running replica query doesn't survive the primary teardown.
	pools := []*pgxpool.Pool{d.pool}
	for _, e := range d.readPools {
		if e.pool != nil {
			pools = append(pools, e.pool)
		}
	}

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		if allIdle(pools) {
			d.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			d.Close()
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// allIdle returns true when every pool has zero acquired
// connections — the safe shutdown point.
func allIdle(pools []*pgxpool.Pool) bool {
	for _, p := range pools {
		if p.Stat().AcquiredConns() > 0 {
			return false
		}
	}
	return true
}
