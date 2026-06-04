package jobs

import (
	"context"
	"time"

	"github.com/theizzatbek/gokit/db"
	xerrs "github.com/theizzatbek/gokit/errs"
)

// Stable Code constants for operator helpers.
const (
	// CodeJobNotFound — Cancel could not find the supplied id in the
	// queued set (already running / done / failed / cancelled / row
	// never existed).
	CodeJobNotFound = "jobs_not_found"

	// CodeOpFailed — the underlying UPDATE / SELECT returned a DB
	// error.
	CodeOpFailed = "jobs_op_failed"
)

// Cancel marks a queued job as cancelled — the worker's claim SQL
// only fetches state='queued' rows, so a cancelled job stays in
// the table for audit but never dispatches.
//
// Use when an operator (or admin endpoint) needs to abort a scheduled
// future job (e.g. user unsubscribed from the email job that was
// scheduled to fire in 24h). Cannot cancel an already-running job —
// the handler is in-flight on a worker.
//
// Returns *errs.Error{KindNotFound, Code: CodeJobNotFound} when:
//   - the row does not exist, OR
//   - the row is in a state other than 'queued' (running / done /
//     failed / cancelled).
//
// Cancel works on any [db.Querier] so it can be wrapped in a tx
// alongside business state changes.
func Cancel(ctx context.Context, q db.Querier, id int64) error {
	if q == nil {
		return xerrs.Validation(CodeNilDB, "jobs: nil Querier")
	}
	const sql = `
		UPDATE jobs
		SET state = 'cancelled', finished_at = NOW()
		WHERE id = $1 AND state = 'queued'
	`
	tag, err := q.Exec(ctx, sql, id)
	if err != nil {
		return xerrs.Wrap(err, xerrs.KindInternal, CodeOpFailed,
			"jobs: Cancel")
	}
	if tag.RowsAffected() == 0 {
		return xerrs.NotFoundf(CodeJobNotFound,
			"jobs: no queued job with id %d", id)
	}
	return nil
}

// Stats is a point-in-time queue snapshot for /admin endpoints. One
// SELECT, partial-index-backed.
//
// Field semantics:
//   - Queued       — count(state='queued') — includes
//     eligible-now AND future-scheduled / backed-off.
//   - Eligible     — count(state='queued' AND run_at <= NOW())
//     — what the next worker tick would claim.
//   - Running      — count(state='running') — handlers currently
//     in-flight on any worker replica.
//   - Failed       — count(state='failed') — exhausted max_attempts.
//   - Cancelled    — count(state='cancelled') — operator-aborted.
//   - Done         — count(state='done') — successful completions
//     still in the table (jobs has no built-in
//     retention; operator deletes when needed).
//   - OldestQueued — MIN(run_at) FILTER (state='queued') — when the
//     head of the queue was scheduled. Zero when no
//     rows are queued.
type Stats struct {
	Queued       int
	Eligible     int
	Running      int
	Failed       int
	Cancelled    int
	Done         int
	OldestQueued time.Time
}

// CodeStatsFailed — GatherStats's aggregate query errored.
const CodeStatsFailed = "jobs_stats_failed"

// GatherStats issues one aggregate SELECT against the jobs table.
// Cheap — covered by the partial-index on (queue, priority DESC,
// run_at) for the queued-state filters; the running/failed/done
// filters fall back to seq-scans on small subsets.
//
// Errors flow as *errs.Error{KindInternal, Code: CodeStatsFailed}
// so /admin handlers can switch on the Code for degraded responses.
func GatherStats(ctx context.Context, q db.Querier) (Stats, error) {
	if q == nil {
		return Stats{}, xerrs.Validation(CodeNilDB, "jobs: nil Querier")
	}
	const sql = `
		SELECT
			count(*) FILTER (WHERE state='queued'),
			count(*) FILTER (WHERE state='queued' AND run_at <= NOW()),
			count(*) FILTER (WHERE state='running'),
			count(*) FILTER (WHERE state='failed'),
			count(*) FILTER (WHERE state='cancelled'),
			count(*) FILTER (WHERE state='done'),
			MIN(run_at) FILTER (WHERE state='queued')
		FROM jobs
	`
	var (
		s            Stats
		oldestQueued *time.Time
	)
	row := q.QueryRow(ctx, sql)
	if err := row.Scan(
		&s.Queued, &s.Eligible, &s.Running,
		&s.Failed, &s.Cancelled, &s.Done,
		&oldestQueued,
	); err != nil {
		return Stats{}, xerrs.Wrap(err, xerrs.KindInternal, CodeStatsFailed,
			"jobs: GatherStats")
	}
	if oldestQueued != nil {
		s.OldestQueued = *oldestQueued
	}
	return s, nil
}
