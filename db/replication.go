package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// ReplicaLagInfo is the per-replica replication-lag projection
// returned by [DB.ReplicationLag]. Snapshot at the moment of the
// query — not live; re-query for fresh values.
type ReplicaLagInfo struct {
	// PoolName matches the `pool=` label used in the kit's Prometheus
	// collectors ("standby" for the single-replica back-compat path,
	// "standby-N" for [Config.ReadURLs] entries in index order).
	PoolName string

	// LagSeconds is the wall-clock seconds the replica is behind the
	// primary, derived from `now() - pg_last_xact_replay_timestamp()`.
	// Zero for the no-data case (newly-started replica that has not
	// yet replayed any transaction) AND for non-replica nodes (the
	// system function returns NULL on the primary — kit treats that
	// as 0 + Healthy=true so the gauge stays sensible).
	LagSeconds float64

	// Healthy is false when the lag query failed (network error,
	// connection drop, server not accepting reads). Err carries the
	// underlying pgx error for diagnostics. A non-healthy entry has
	// LagSeconds=0 — do NOT treat zero as "caught up" without
	// checking Healthy first.
	Healthy bool

	// Err is non-nil iff Healthy is false.
	Err error
}

// ReplicationLag queries every configured read-replica pool in
// parallel and returns a snapshot ordered by pool name. Returns an
// empty slice (not an error) when no replica is configured.
//
// The kit does NOT cache results — call sparingly from admin endpoints
// / scheduled probes. For continuous monitoring, wire
// [WithReplicaLagPolling] which runs a background goroutine and emits
// the kit-managed gauge.
func (d *DB) ReplicationLag(ctx context.Context) []ReplicaLagInfo {
	if len(d.readPools) == 0 {
		return []ReplicaLagInfo{}
	}
	out := make([]ReplicaLagInfo, len(d.readPools))
	for i, e := range d.readPools {
		out[i] = queryReplicaLag(ctx, e.name, e.pool)
	}
	return out
}

// poolReader is the minimal Querier-ish surface queryReplicaLag needs.
// Lets tests substitute a stub without standing up a real pgxpool.
type poolReader interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// queryReplicaLag executes the kit's standard lag query against one
// pool and translates the outcome into a ReplicaLagInfo. The function
// is split out so the polling goroutine can reuse it.
//
// `pg_last_xact_replay_timestamp()` is null on the primary; we treat
// null as "lag=0, healthy" — same shape a freshly-caught-up replica
// would report.
func queryReplicaLag(ctx context.Context, name string, p poolReader) ReplicaLagInfo {
	const sql = `SELECT EXTRACT(EPOCH FROM (NOW() - pg_last_xact_replay_timestamp()))::float8`
	var lag *float64
	if err := p.QueryRow(ctx, sql).Scan(&lag); err != nil {
		return ReplicaLagInfo{
			PoolName: name,
			Healthy:  false,
			Err:      err,
		}
	}
	out := ReplicaLagInfo{PoolName: name, Healthy: true}
	if lag != nil && *lag > 0 {
		out.LagSeconds = *lag
	}
	return out
}

// startLagPolling boots the background lag-polling goroutine when
// WithReplicaLagPolling was wired AND at least one replica is
// configured. Both invariants are enforced by the caller (Connect).
func (d *DB) startLagPolling() {
	ctx, cancel := context.WithCancel(context.Background())
	d.lagPoll.cancel = cancel
	d.lagPoll.done = make(chan struct{})
	go d.runLagPolling(ctx)
}

// stopLagPolling cancels the polling goroutine and waits for it to
// drain. Idempotent — multiple Close calls are safe.
func (d *DB) stopLagPolling() {
	d.lagPoll.stopOnce.Do(func() {
		if d.lagPoll.cancel != nil {
			d.lagPoll.cancel()
		}
		if d.lagPoll.done != nil {
			<-d.lagPoll.done
		}
	})
}

// runLagPolling is the polling loop. Ticker-driven; on each tick it
// queries every replica, updates the metric gauge (when wired), and
// optionally logs a WARN per replica whose lag exceeds the configured
// threshold.
func (d *DB) runLagPolling(ctx context.Context) {
	defer close(d.lagPoll.done)
	interval := d.opts.lagPoll.interval
	threshold := d.opts.lagPoll.threshold
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	d.tickLagPolling(ctx, threshold) // immediate first sample
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.tickLagPolling(ctx, threshold)
		}
	}
}

// tickLagPolling runs one polling pass — one lag-query per replica,
// followed by metric updates + threshold-warn logging. The per-pool
// query gets a bounded sub-context so a stuck replica does not block
// the whole tick.
func (d *DB) tickLagPolling(ctx context.Context, threshold time.Duration) {
	for _, e := range d.readPools {
		qctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		info := queryReplicaLag(qctx, e.name, e.pool)
		cancel()

		if !info.Healthy {
			if d.opts.logger != nil {
				d.opts.logger.Warn("db: replica lag query failed",
					"pool", e.name, "err", info.Err)
			}
			// Mark gauge as -1 so dashboards can distinguish "unknown"
			// from "caught up" (a 0 sample means the replica answered
			// with lag=0). Zero would otherwise mis-paint a dead replica
			// as healthy.
			d.opts.metrics.setReplicaLag(e.name, -1)
			continue
		}
		d.opts.metrics.setReplicaLag(e.name, info.LagSeconds)
		if threshold > 0 && info.LagSeconds > threshold.Seconds() {
			if d.opts.logger != nil {
				d.opts.logger.Warn("db: replica lag above threshold",
					"pool", e.name,
					"lag_seconds", info.LagSeconds,
					"threshold_seconds", threshold.Seconds())
			}
		}
	}
}
