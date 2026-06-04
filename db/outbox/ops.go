package outbox

import (
	"context"

	"github.com/google/uuid"

	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/errs"
)

// Stable Code constants for the operator helpers.
const (
	// CodeOpNotFound — RetryNow / ResetAttempts could not find a
	// matching unpublished row. Replay does NOT return this on a
	// missing ID — it's a bulk operation that silently skips misses.
	CodeOpNotFound = "outbox_op_not_found"

	// CodeOpFailed — the underlying UPDATE errored (conn drop, etc.).
	CodeOpFailed = "outbox_op_failed"
)

// RetryNow makes the supplied event eligible for the next drain tick
// by setting next_retry_at = NOW(). The event must still be
// unpublished — RetryNow on an already-published row returns
// *errs.Error{KindNotFound, Code: CodeOpNotFound} so an operator's
// "retry this!" runbook fails loud instead of silently re-publishing.
//
// Use when ops sees a stuck backed-off event in the queue and wants
// it tried immediately without waiting for the exponential-backoff
// window to close.
func RetryNow(ctx context.Context, q db.Querier, id uuid.UUID) error {
	const sql = `
		UPDATE outbox
		SET next_retry_at = NOW()
		WHERE id = $1 AND published_at IS NULL
	`
	tag, err := q.Exec(ctx, sql, id)
	if err != nil {
		return errs.Wrap(err, errs.KindInternal, CodeOpFailed,
			"outbox: RetryNow")
	}
	if tag.RowsAffected() == 0 {
		return errs.NotFoundf(CodeOpNotFound,
			"outbox: no unpublished event with id %s", id)
	}
	return nil
}

// Replay re-dispatches already-published events by clearing
// published_at + attempts + last_error AND stamping next_retry_at =
// NOW(). Returns the count of rows actually re-armed; missing IDs are
// silently skipped — Replay is a bulk operator action, not a per-row
// guarantee.
//
// Use when a downstream consumer bug was fixed and the operator wants
// to re-deliver a span of recent events. Pair with [ListPublished] or
// an ad-hoc SELECT against (event_type, aggregate_id) to find the
// candidate IDs first.
//
// Idempotency: an event that's already in the unpublished set is
// re-stamped (next_retry_at moves forward); attempts is zeroed. The
// Worker re-emits the event on its next drain — downstream consumers
// MUST be idempotent for this to be safe.
//
// Empty ids returns (0, nil) — no-op.
func Replay(ctx context.Context, q db.Querier, ids ...uuid.UUID) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	const sql = `
		UPDATE outbox
		SET published_at = NULL,
		    attempts = 0,
		    last_error = NULL,
		    next_retry_at = NOW()
		WHERE id = ANY($1::uuid[])
	`
	tag, err := q.Exec(ctx, sql, ids)
	if err != nil {
		return 0, errs.Wrap(err, errs.KindInternal, CodeOpFailed,
			"outbox: Replay")
	}
	return tag.RowsAffected(), nil
}

// ResetAttempts clears attempts + last_error AND stamps next_retry_at
// = NOW() on an unpublished event. Use to flow a row whose attempts
// crossed [WithMaxAttempts] (so the Worker no longer fetches it) back
// into the active queue after a manual investigation / fix.
//
// Returns *errs.Error{KindNotFound, Code: CodeOpNotFound} when the
// row doesn't exist OR is already published.
func ResetAttempts(ctx context.Context, q db.Querier, id uuid.UUID) error {
	const sql = `
		UPDATE outbox
		SET attempts = 0,
		    last_error = NULL,
		    next_retry_at = NOW()
		WHERE id = $1 AND published_at IS NULL
	`
	tag, err := q.Exec(ctx, sql, id)
	if err != nil {
		return errs.Wrap(err, errs.KindInternal, CodeOpFailed,
			"outbox: ResetAttempts")
	}
	if tag.RowsAffected() == 0 {
		return errs.NotFoundf(CodeOpNotFound,
			"outbox: no unpublished event with id %s", id)
	}
	return nil
}
