package outbox

import (
	"context"
	"encoding/json"

	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/errs"
)

// CodeListFailed — ListPending / ListDead's SELECT errored.
const CodeListFailed = "outbox_list_failed"

// ListPending returns up to limit unpublished events, ordered by the
// same (next_retry_at, created_at) tuple the Worker drains in. Each
// returned [Event] is fully hydrated, payload included.
//
// Use for /admin queue-inspection endpoints — what is the queue
// about to dispatch, what's stuck at the head? For high-frequency
// reads (dashboards refresh on a tick), prefer [GatherStats].
//
// limit ≤ 0 returns (nil, nil) — guards against accidental "fetch
// the whole queue" calls. Pass a modest cap (≤100); payload bytes
// are loaded into memory.
func ListPending(ctx context.Context, q db.Querier, limit int) ([]Event, error) {
	if limit <= 0 {
		return nil, nil
	}
	const sql = `
		SELECT id, aggregate_type, aggregate_id, event_type, payload,
		       headers, created_at, attempts, COALESCE(last_error, '')
		FROM outbox
		WHERE published_at IS NULL
		ORDER BY next_retry_at, created_at
		LIMIT $1
	`
	return queryEvents(ctx, q, sql, limit)
}

// ListDead returns up to limit unpublished events with attempts >=
// maxAttempts — the "dead-letter" filter the Worker uses to stop
// fetching. maxAttempts MUST match [WithMaxAttempts] on the running
// Worker for the result to align with the live drain.
//
// Use for /admin dead-letter inspection: which events crossed the
// retry budget, what was last_error, when did they enter the queue?
// Pair with [Replay] / [ResetAttempts] to recover individual rows
// once the underlying issue is fixed.
//
// limit ≤ 0 returns (nil, nil); maxAttempts ≤ 0 returns (nil, nil)
// because there's no dead-letter set when retries are unlimited.
func ListDead(ctx context.Context, q db.Querier, limit, maxAttempts int) ([]Event, error) {
	if limit <= 0 || maxAttempts <= 0 {
		return nil, nil
	}
	const sql = `
		SELECT id, aggregate_type, aggregate_id, event_type, payload,
		       headers, created_at, attempts, COALESCE(last_error, '')
		FROM outbox
		WHERE published_at IS NULL AND attempts >= $1
		ORDER BY next_retry_at, created_at
		LIMIT $2
	`
	return queryEvents(ctx, q, sql, maxAttempts, limit)
}

// queryEvents is the shared scan loop for ListPending / ListDead.
// Both queries share the same projection shape; only the WHERE
// clause differs.
func queryEvents(ctx context.Context, q db.Querier, sql string, args ...any) ([]Event, error) {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, errs.Wrap(err, errs.KindInternal, CodeListFailed,
			"outbox: list events")
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var (
			e           Event
			headersJSON []byte
		)
		if err := rows.Scan(
			&e.ID, &e.AggregateType, &e.AggregateID, &e.EventType, &e.Payload,
			&headersJSON, &e.CreatedAt, &e.Attempts, &e.LastError,
		); err != nil {
			return nil, errs.Wrap(err, errs.KindInternal, CodeListFailed,
				"outbox: scan row")
		}
		if len(headersJSON) > 0 {
			if jerr := json.Unmarshal(headersJSON, &e.Headers); jerr != nil {
				// Malformed headers cell — drop headers, surface row.
				// Same convention as selectBatch.
				e.Headers = nil
			}
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, errs.Wrap(err, errs.KindInternal, CodeListFailed,
			"outbox: rows.Err")
	}
	return out, nil
}
