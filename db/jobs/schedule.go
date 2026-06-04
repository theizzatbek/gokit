package jobs

import (
	"context"
	_ "embed"
	"encoding/json"
	"time"

	"github.com/theizzatbek/gokit/db"
	xerrs "github.com/theizzatbek/gokit/errs"
)

// Schedule reads from a [db.Querier] so the call can run inside a
// [db.DB.Tx]-wrapped transaction (transactional enqueue —
// "Schedule(jobs)" + the row-write that triggers it commit together).

//go:embed schema.sql
var schemaDDL string

// Schema returns the DDL bundled in the package. Mostly used by
// service.WithJobsAutoSchema or test fixtures; production
// deployments typically include this in their migration tool.
func Schema() string { return schemaDDL }

// ApplySchema executes [Schema] against the supplied DB. Safe to call
// repeatedly (every statement is IF NOT EXISTS).
func ApplySchema(ctx context.Context, d *db.DB) error {
	if d == nil {
		return xerrs.Validation(CodeNilDB, "jobs: nil DB")
	}
	if _, err := d.Exec(ctx, schemaDDL); err != nil {
		return xerrs.Wrap(err, xerrs.KindInternal, CodeSchemaApplyFailed,
			"jobs: apply schema failed")
	}
	return nil
}

// ScheduleOption tunes one Schedule call.
type ScheduleOption func(*scheduleOptions)

type scheduleOptions struct {
	queue       string
	maxAttempts int
	priority    int
	dedupKey    string
	hasDedup    bool
}

// WithQueue assigns the job to a named queue. Default "default".
// Workers can be configured to drain a subset of queues via
// [WithQueues].
func WithQueue(name string) ScheduleOption {
	return func(o *scheduleOptions) { o.queue = name }
}

// WithMaxAttempts overrides the per-row retry cap. Default 25 (chosen
// to match common production retry budgets — ~24h of exponential
// backoff finishes well within that ceiling).
func WithMaxAttempts(n int) ScheduleOption {
	return func(o *scheduleOptions) { o.maxAttempts = n }
}

// WithPriority stamps a numeric priority on the job. The Worker
// ORDER-BYs priority DESC before run_at, so higher numbers run
// first within the same eligibility window. Default 0 — equal
// priority falls through to run_at ordering.
//
// Use modest spreads (0 / 10 / 100) rather than tightly-clustered
// distinct values per job — the partial index keyed on
// (queue, priority DESC, run_at) only stays efficient when
// priorities form a small set.
func WithPriority(n int) ScheduleOption {
	return func(o *scheduleOptions) { o.priority = n }
}

// WithDedupKey makes the Schedule call idempotent against an
// already-queued job of the same type. The unique partial index
// `idx_jobs_dedup_queued` covers (type, dedup_key) WHERE state =
// 'queued' so a second Schedule with the same key while the first
// is still queued returns the EXISTING job's ID instead of inserting.
//
// Cancelled / done / failed jobs leave the partial index, so
// re-scheduling the same dedup_key after a previous run completed
// always inserts a fresh row — the dedupe is "don't pile up
// duplicates in the queue", not "ever process this key only once".
// Use [db/inbox] for the latter (effectively-once consumer dedup).
//
//	id, _ := jobs.Schedule(ctx, svc.DB,
//	    time.Now().Add(time.Hour),
//	    "billing.send-invoice",
//	    Invoice{UserID: "u-42", Month: "2026-06"},
//	    jobs.WithDedupKey("u-42:2026-06"),
//	)
//
// Empty key is treated as "no dedupe" — same as omitting the option.
func WithDedupKey(key string) ScheduleOption {
	return func(o *scheduleOptions) {
		if key == "" {
			return
		}
		o.dedupKey = key
		o.hasDedup = true
	}
}

// Plain INSERT path — used when no dedup_key is supplied.
const insertSQL = `
INSERT INTO jobs (type, queue, payload, run_at, max_attempts, priority)
VALUES ($1, $2, $3::jsonb, $4, $5, $6)
RETURNING id
`

// Dedup-aware insert path. INSERT ... ON CONFLICT DO NOTHING on the
// partial unique index; the CTE-UNION pattern returns the inserted
// id when the row was new OR the existing id when the unique
// constraint suppressed the insert.
const insertDedupSQL = `
WITH ins AS (
    INSERT INTO jobs (type, queue, payload, run_at, max_attempts, priority, dedup_key)
    VALUES ($1, $2, $3::jsonb, $4, $5, $6, $7)
    ON CONFLICT (type, dedup_key) WHERE state = 'queued' AND dedup_key IS NOT NULL
    DO NOTHING
    RETURNING id
)
SELECT id FROM ins
UNION ALL
SELECT id FROM jobs
 WHERE type = $1 AND dedup_key = $7 AND state = 'queued'
 LIMIT 1
`

// Schedule inserts one job. Use runAt = time.Now() (or zero time) for
// "run as soon as possible" — the Worker polls at WithInterval cadence.
//
// payload is JSON-encoded; pass a typed struct, map, or any value
// that survives encoding/json.Marshal.
//
//	id, _ := jobs.Schedule(ctx, svc.DB,
//	    time.Now().Add(15*time.Minute),
//	    "user.welcome",
//	    Welcome{UserID: "u-42"},
//	    jobs.WithMaxAttempts(5))
//
// Pass [WithDedupKey] to make the call idempotent when a job of the
// same type is already queued — the returned ID is the existing
// row's, not a fresh insert.
func Schedule[T any](ctx context.Context, q db.Querier, runAt time.Time, jobType string, payload T, opts ...ScheduleOption) (int64, error) {
	if q == nil {
		return 0, xerrs.Validation(CodeNilDB, "jobs: nil Querier")
	}
	if jobType == "" {
		return 0, xerrs.Validation(CodeInvalidJobType, "jobs: jobType is required")
	}
	o := scheduleOptions{queue: "default", maxAttempts: 25}
	for _, opt := range opts {
		opt(&o)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return 0, xerrs.Wrap(err, xerrs.KindValidation, CodePayloadEncode,
			"jobs: payload encode failed")
	}
	if runAt.IsZero() {
		runAt = time.Now()
	}
	var id int64
	if o.hasDedup {
		row := q.QueryRow(ctx, insertDedupSQL,
			jobType, o.queue, raw, runAt, o.maxAttempts, o.priority, o.dedupKey)
		if err := row.Scan(&id); err != nil {
			return 0, xerrs.Wrap(err, xerrs.KindInternal, CodeInsertFailed,
				"jobs: insert failed")
		}
		return id, nil
	}
	row := q.QueryRow(ctx, insertSQL,
		jobType, o.queue, raw, runAt, o.maxAttempts, o.priority)
	if err := row.Scan(&id); err != nil {
		return 0, xerrs.Wrap(err, xerrs.KindInternal, CodeInsertFailed,
			"jobs: insert failed")
	}
	return id, nil
}
