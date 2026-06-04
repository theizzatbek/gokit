package db

import (
	"context"

	"github.com/theizzatbek/gokit/errs"
)

// Healthcheck pings the primary pool with "SELECT 1" under the caller's
// context. The caller is responsible for the deadline: wrap with
// context.WithTimeout to avoid hanging on a dead pool. Returns
// *errs.Error{Kind: Unavailable} on any failure (closed pool, network
// error, server down).
//
// HasReadReplica=true does NOT make Healthcheck ping the standby; use
// HealthcheckRead for that. Splitting the calls lets /readyz fail the
// process only on primary loss (ReadQuery transparently falls back to
// primary) while /healthz can report standby state separately.
func (d *DB) Healthcheck(ctx context.Context) error {
	if d.pool == nil {
		return errs.Unavailable("db_unavailable", "db pool is closed")
	}
	if _, err := d.pool.Exec(ctx, "SELECT 1"); err != nil {
		return errs.Wrap(err, errs.KindUnavailable, "db_unavailable", "healthcheck failed")
	}
	return nil
}

// HealthcheckRead pings every configured read-replica pool with
// "SELECT 1". Returns nil when no replica is configured — a no-standby
// deployment is healthy by definition for the read-only path. On
// failure, the returned *errs.Error carries the failing pool name in
// its message so operators can identify which standby is down.
//
// Use to detect silent standby loss: ReadQuery transparently falls back
// to the primary when no replica is configured, but it does NOT detect
// a stuck or half-dead standby connection that times out mid-query.
// Calling HealthcheckRead from /healthz (or a scheduled check) surfaces
// that before a user-facing read query hits the timeout.
//
// Multi-replica semantics: the method returns the FIRST failure
// observed; remaining pools are NOT pinged in that case (the caller
// can call again after fixing the surfaced replica). Use
// [DB.ReplicationLag] for a per-pool snapshot that always pings all
// replicas regardless of individual outcome.
func (d *DB) HealthcheckRead(ctx context.Context) error {
	if len(d.readPools) == 0 {
		return nil
	}
	for _, e := range d.readPools {
		if _, err := e.pool.Exec(ctx, "SELECT 1"); err != nil {
			return errs.Wrap(err, errs.KindUnavailable, "db_unavailable",
				"read-replica healthcheck failed: pool="+e.name)
		}
	}
	return nil
}
