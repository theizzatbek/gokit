package outbox

import (
	"context"
	"time"

	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/errs"
)

// Stats is a point-in-time outbox snapshot for /admin endpoints. One
// SELECT, no joins — cheap enough to hit on every /admin request.
//
// Field semantics:
//   - Pending      — count of unpublished rows (published_at IS NULL)
//     INCLUDING failed-and-backed-off rows. Matches
//     the Worker's drain set ignoring next_retry_at.
//   - Eligible     — count of rows the Worker would fetch on the
//     next tick (published_at IS NULL AND
//     next_retry_at <= NOW()).
//   - Failed       — count of unpublished rows with attempts > 0.
//     Subset of Pending. Indicates downstream trouble.
//   - OldestPending — created_at of the oldest unpublished row.
//     Zero time when nothing is pending.
//   - Published1m  — count of rows published in the last minute.
//     Approximates throughput for /admin dashboards.
type Stats struct {
	Pending       int
	Eligible      int
	Failed        int
	OldestPending time.Time
	Published1m   int
}

// CodeStatsFailed — the aggregate query errored.
const CodeStatsFailed = "outbox_stats_failed"

// GatherStats issues one aggregate SELECT against the outbox table.
// Errors propagate as *errs.Error{Kind: KindInternal, Code:
// CodeStatsFailed} so /admin handlers can switch on the Code for
// degraded responses.
//
// Performance: the query uses the existing outbox_pending_idx for
// the pending/eligible/failed counts; Published1m is a sequential
// scan when the table is large (no index on published_at). If your
// outbox stays under ~1M rows this is comfortable; beyond that,
// add `CREATE INDEX ... ON outbox(published_at)` to make this O(log n).
func GatherStats(ctx context.Context, q db.Querier) (Stats, error) {
	const sql = `
		SELECT
			count(*) FILTER (WHERE published_at IS NULL),
			count(*) FILTER (WHERE published_at IS NULL AND next_retry_at <= NOW()),
			count(*) FILTER (WHERE published_at IS NULL AND attempts > 0),
			MIN(created_at) FILTER (WHERE published_at IS NULL),
			count(*) FILTER (WHERE published_at >= NOW() - INTERVAL '1 minute')
		FROM outbox
	`
	var (
		s             Stats
		oldestPending *time.Time
	)
	row := q.QueryRow(ctx, sql)
	if err := row.Scan(&s.Pending, &s.Eligible, &s.Failed, &oldestPending, &s.Published1m); err != nil {
		return Stats{}, errs.Wrap(err, errs.KindInternal, CodeStatsFailed,
			"outbox: GatherStats")
	}
	if oldestPending != nil {
		s.OldestPending = *oldestPending
	}
	return s, nil
}
