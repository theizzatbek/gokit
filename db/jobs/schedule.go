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

const insertSQL = `
INSERT INTO jobs (type, queue, payload, run_at, max_attempts)
VALUES ($1, $2, $3::jsonb, $4, $5)
RETURNING id
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
	row := q.QueryRow(ctx, insertSQL, jobType, o.queue, raw, runAt, o.maxAttempts)
	if err := row.Scan(&id); err != nil {
		return 0, xerrs.Wrap(err, xerrs.KindInternal, CodeInsertFailed,
			"jobs: insert failed")
	}
	return id, nil
}
