package inbox

import (
	"context"
	"time"

	"github.com/theizzatbek/gokit/db"
	xerrs "github.com/theizzatbek/gokit/errs"
)

// CodeBatchEmpty — ProcessBatch was called with an empty keys slice.
// Returns a Validation error so a caller passing an empty NATS pull
// batch fails loud instead of silently no-op-ing inside a tx.
const CodeBatchEmpty = "inbox_batch_empty"

// ProcessBatch is the bulk variant of [Process]. It runs ALL inserts
// AND fn inside a single transaction:
//
//  1. Validates each Key (Consumer + EventID non-empty).
//  2. INSERTs every (consumer, event_id) row with ON CONFLICT DO
//     NOTHING — Postgres reports back which rows were inserted via
//     `RETURNING event_id`.
//  3. Builds a parallel []Outcome (same length as keys) marking each
//     position [OutcomeProcessed] or [OutcomeDuplicate].
//  4. Calls fn with the tx and the indices of the newly-inserted
//     keys. fn handles only the NEW items; duplicates were already
//     processed in a prior delivery.
//
// On fn error the transaction rolls back — inbox rows AND any
// side-effects fn made are undone, so the redelivery retries
// cleanly. On success the returned []Outcome aligns 1-to-1 with
// keys so callers can ack the redelivery atomically.
//
// Use when a NATS pull subscription delivers a batch of N messages
// and the consumer wants ONE round-trip per batch instead of N
// per-message Process calls. Typical 10–50x speed-up for high-fanout
// projections.
//
// Empty keys returns *errs.Error{KindValidation, CodeBatchEmpty}.
//
// Note: each batch is one transaction. The transaction holds the
// inbox rows + any state fn writes until commit; keep batches
// modest (a few hundred at most) to avoid long-held locks.
func ProcessBatch(
	ctx context.Context,
	d *db.DB,
	keys []Key,
	fn func(tx *db.Tx, newIdx []int) error,
) ([]Outcome, error) {
	return (&Inbox{}).ProcessBatch(ctx, d, keys, fn)
}

// ProcessBatch is the [Inbox] method counterpart. Identical semantics
// to the package-level [ProcessBatch] but emits per-consumer logger +
// metrics from cfg.
func (in *Inbox) ProcessBatch(
	ctx context.Context,
	d *db.DB,
	keys []Key,
	fn func(tx *db.Tx, newIdx []int) error,
) ([]Outcome, error) {
	if len(keys) == 0 {
		return nil, xerrs.Validation(CodeBatchEmpty,
			"inbox: ProcessBatch: keys is empty")
	}
	// Pre-validate every key before opening the transaction — a
	// single bad key shouldn't waste a tx slot.
	for i, k := range keys {
		if k.Consumer == "" {
			return nil, xerrs.Validationf(CodeMissingConsumer,
				"inbox: ProcessBatch: keys[%d].Consumer is empty", i)
		}
		if k.EventID == "" {
			return nil, xerrs.Validationf(CodeMissingEventID,
				"inbox: ProcessBatch: keys[%d].EventID is empty", i)
		}
	}
	start := time.Now()
	outcomes := make([]Outcome, len(keys))
	for i := range outcomes {
		outcomes[i] = OutcomeDuplicate // default — flip to Processed below
	}
	err := d.Tx(ctx, func(tx *db.Tx) error {
		// Build per-(consumer, event_id) index lookup so the RETURNING
		// scan can mark the right position back to OutcomeProcessed.
		// Keys can repeat in the slice (e.g. retried delivery duplicate
		// within the same batch) — we map each key to its FIRST index.
		type k2 struct {
			c, e string
		}
		idxOf := make(map[k2]int, len(keys))
		for i, k := range keys {
			kk := k2{k.Consumer, k.EventID}
			if _, ok := idxOf[kk]; !ok {
				idxOf[kk] = i
			}
		}

		// Bulk insert via UNNEST so we issue one round-trip regardless
		// of batch size. ON CONFLICT DO NOTHING lets duplicates fall
		// through silently; RETURNING streams back only the inserted
		// rows so we know which keys were "first delivery".
		consumers := make([]string, len(keys))
		eventIDs := make([]string, len(keys))
		for i, k := range keys {
			consumers[i] = k.Consumer
			eventIDs[i] = k.EventID
		}
		const sql = `
			INSERT INTO inbox (consumer, event_id)
			SELECT * FROM UNNEST($1::text[], $2::text[])
			ON CONFLICT DO NOTHING
			RETURNING consumer, event_id
		`
		rows, err := tx.Query(ctx, sql, consumers, eventIDs)
		if err != nil {
			return err
		}
		defer rows.Close()
		var newIdx []int
		for rows.Next() {
			var c, e string
			if err := rows.Scan(&c, &e); err != nil {
				return err
			}
			if i, ok := idxOf[k2{c, e}]; ok {
				outcomes[i] = OutcomeProcessed
				newIdx = append(newIdx, i)
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if fn == nil || len(newIdx) == 0 {
			return nil
		}
		return fn(tx, newIdx)
	})
	if err != nil {
		// Tag every position as a tx failure for observability — caller
		// gets a single err return; the outcomes slice is informational.
		in.observe("batch", "error", time.Since(start))
		return nil, xerrs.Wrap(err, xerrs.KindInternal, CodeTxFailed,
			"inbox: ProcessBatch: tx failed")
	}
	// Aggregate metric: one observe per call labelled by the dominant
	// outcome (any Processed → "processed", else "duplicate"). Matches
	// the single-Process observability shape.
	dominant := OutcomeDuplicate.String()
	for _, o := range outcomes {
		if o == OutcomeProcessed {
			dominant = OutcomeProcessed.String()
			break
		}
	}
	in.observe("batch", dominant, time.Since(start))
	return outcomes, nil
}

// Exists reports whether (consumer, event_id) is already recorded.
// Pure check — no INSERT. Use for read-side validation in handlers
// that don't want the dedup INSERT side-effect (the consumer
// processes the message elsewhere and only needs to know if it was
// seen).
//
// Returns *errs.Error{KindValidation, ...} for empty Consumer/EventID
// — matches the validation semantics of [Process].
func Exists(ctx context.Context, q db.Querier, key Key) (bool, error) {
	if key.Consumer == "" {
		return false, xerrs.Validation(CodeMissingConsumer,
			"inbox: Exists: Key.Consumer is required")
	}
	if key.EventID == "" {
		return false, xerrs.Validation(CodeMissingEventID,
			"inbox: Exists: Key.EventID is required")
	}
	const sql = `SELECT EXISTS(SELECT 1 FROM inbox WHERE consumer = $1 AND event_id = $2)`
	var exists bool
	if err := q.QueryRow(ctx, sql, key.Consumer, key.EventID).Scan(&exists); err != nil {
		return false, xerrs.Wrap(err, xerrs.KindInternal, CodeTxFailed,
			"inbox: Exists: query failed")
	}
	return exists, nil
}

// MarkProcessed records the key as processed WITHOUT running a
// handler. Use when the consumer side-effect already happened
// elsewhere (e.g. an external system confirmed delivery) and you
// just need to register the receipt for future-redelivery dedup.
//
// Returns:
//   - [OutcomeProcessed] when the row was newly inserted.
//   - [OutcomeDuplicate] when the row was already present.
//
// Caller-validation errors and underlying tx errors surface the
// same Codes as [Process].
func MarkProcessed(ctx context.Context, q db.Querier, key Key) (Outcome, error) {
	if key.Consumer == "" {
		return 0, xerrs.Validation(CodeMissingConsumer,
			"inbox: MarkProcessed: Key.Consumer is required")
	}
	if key.EventID == "" {
		return 0, xerrs.Validation(CodeMissingEventID,
			"inbox: MarkProcessed: Key.EventID is required")
	}
	const sql = `INSERT INTO inbox (consumer, event_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`
	tag, err := q.Exec(ctx, sql, key.Consumer, key.EventID)
	if err != nil {
		return 0, xerrs.Wrap(err, xerrs.KindInternal, CodeTxFailed,
			"inbox: MarkProcessed: insert failed")
	}
	if tag.RowsAffected() == 0 {
		return OutcomeDuplicate, nil
	}
	return OutcomeProcessed, nil
}
