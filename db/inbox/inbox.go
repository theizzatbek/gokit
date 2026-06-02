package inbox

import (
	"context"
	_ "embed"
	"time"

	"github.com/theizzatbek/gokit/db"
	xerrs "github.com/theizzatbek/gokit/errs"
)

//go:embed schema.sql
var schemaSQL string

// Schema returns the DDL for the inbox table + indexes. Idempotent —
// safe to run on every startup.
func Schema() string { return schemaSQL }

// Key uniquely identifies one (consumer, event) pair. Both fields
// required; empty values fail Process with [CodeMissingConsumer] /
// [CodeMissingEventID].
type Key struct {
	// Consumer is the caller-supplied dedup namespace. A typical
	// convention is "<service>:<handler>" (e.g.
	// "orders-svc:link.created"). Two callers sharing the same string
	// share the same dedup namespace — that may be a feature or a
	// bug; inbox does not police it.
	Consumer string

	// EventID is the upstream message identifier — Nats-Msg-Id,
	// outbox UUID, Kafka key, etc. The kit treats it as opaque text;
	// uniqueness is enforced within Consumer.
	EventID string
}

// Outcome is the result of a [Process] call.
type Outcome int

const (
	// OutcomeProcessed means fn ran and the transaction committed —
	// this delivery was the first for (Consumer, EventID).
	OutcomeProcessed Outcome = iota

	// OutcomeDuplicate means the (Consumer, EventID) row already
	// existed; fn did NOT run. Caller should ack the redelivery and
	// move on.
	OutcomeDuplicate
)

// String returns the lowercase canonical name used in metric labels.
func (o Outcome) String() string {
	switch o {
	case OutcomeProcessed:
		return "processed"
	case OutcomeDuplicate:
		return "duplicate"
	}
	return "unknown"
}

// Inbox is the optional observability-carrying handle. Construct via
// [New]; per-call Process API is identical to the package-level
// [Process] but emits logger + metrics from cfg.
//
// (*Inbox)(nil) is a safe no-op for Logger/Metrics — the handle just
// degrades to package-level Process semantics.
type Inbox struct {
	cfg        Config
	collectors *metricsCollector
}

// New validates cfg (no required fields — both Logger and Metrics
// are optional) and returns an Inbox handle. Registers metric
// collectors on cfg.Metrics if non-nil.
func New(cfg Config) *Inbox {
	in := &Inbox{cfg: cfg}
	if cfg.Metrics != nil {
		in.collectors = newMetricsCollector(cfg.Metrics)
	}
	return in
}

// Process is the package-level entry point. Equivalent to
// New(Config{}).Process — no logger, no metrics.
func Process(ctx context.Context, d *db.DB, key Key, fn func(*db.Tx) error) (Outcome, error) {
	return (&Inbox{}).Process(ctx, d, key, fn)
}

// Process runs fn inside a transaction if and only if Key was not
// previously recorded.
//
// On success the returned Outcome distinguishes:
//
//   - [OutcomeProcessed] — fn ran and the Tx committed.
//   - [OutcomeDuplicate] — the row already existed; fn was NOT
//     called; nothing was committed apart from the (no-op) Tx itself.
//
// fn's error rolls back the Tx (the inbox row is NOT inserted) AND
// propagates wrapped in *errs.Error{Code: [CodeTxFailed]} so a future
// redelivery runs fn again.
//
// The Tx handle MUST NOT escape fn — pgx invalidates it on commit /
// rollback (same rule as [db.DB.Tx]).
func (in *Inbox) Process(ctx context.Context, d *db.DB, key Key, fn func(*db.Tx) error) (Outcome, error) {
	if key.Consumer == "" {
		return 0, xerrs.Validation(CodeMissingConsumer,
			"inbox: Key.Consumer is required")
	}
	if key.EventID == "" {
		return 0, xerrs.Validation(CodeMissingEventID,
			"inbox: Key.EventID is required")
	}
	start := time.Now()
	var outcome Outcome
	err := d.Tx(ctx, func(tx *db.Tx) error {
		tag, err := tx.Exec(ctx,
			`INSERT INTO inbox (consumer, event_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			key.Consumer, key.EventID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			outcome = OutcomeDuplicate
			return nil
		}
		outcome = OutcomeProcessed
		return fn(tx)
	})
	if err != nil {
		in.observe(key.Consumer, "error", time.Since(start))
		in.logErr(ctx, key, err, time.Since(start))
		return 0, xerrs.Wrapf(err, xerrs.KindInternal, CodeTxFailed,
			"inbox: process %q/%q: tx failed", key.Consumer, key.EventID)
	}
	in.observe(key.Consumer, outcome.String(), time.Since(start))
	in.logOK(ctx, key, outcome, time.Since(start))
	return outcome, nil
}

// observe is the metrics shim — nil-safe via the underlying
// collectors' nil receiver.
func (in *Inbox) observe(consumer, outcome string, dur time.Duration) {
	if in == nil {
		return
	}
	in.collectors.observe(consumer, outcome, dur)
}

func (in *Inbox) logOK(ctx context.Context, key Key, outcome Outcome, dur time.Duration) {
	if in == nil || in.cfg.Logger == nil {
		return
	}
	in.cfg.Logger.DebugContext(ctx, "inbox process",
		"consumer", key.Consumer,
		"event_id", key.EventID,
		"outcome", outcome.String(),
		"duration_ms", dur.Milliseconds())
}

func (in *Inbox) logErr(ctx context.Context, key Key, err error, dur time.Duration) {
	if in == nil || in.cfg.Logger == nil {
		return
	}
	in.cfg.Logger.WarnContext(ctx, "inbox process failed",
		"consumer", key.Consumer,
		"event_id", key.EventID,
		"duration_ms", dur.Milliseconds(),
		"err", err.Error())
}
