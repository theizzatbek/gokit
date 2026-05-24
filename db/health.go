package db

import (
	"context"

	"github.com/theizzatbek/fibermap/errs"
)

// Healthcheck pings the pool with "SELECT 1" under the caller's context.
// The caller is responsible for the deadline: wrap with context.WithTimeout
// to avoid hanging on a dead pool. Returns *errs.Error{Kind: Unavailable}
// on any failure (closed pool, network error, server down).
func (d *DB) Healthcheck(ctx context.Context) error {
	if d.pool == nil {
		return errs.Unavailable("db_unavailable", "db pool is closed")
	}
	if _, err := d.pool.Exec(ctx, "SELECT 1"); err != nil {
		return errs.Wrap(err, errs.KindUnavailable, "db_unavailable", "healthcheck failed")
	}
	return nil
}
