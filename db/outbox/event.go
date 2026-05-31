package outbox

import (
	"context"
	_ "embed"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	natsclient "github.com/theizzatbek/gokit/clients/nats"
	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/errs"
)

//go:embed schema.sql
var schemaSQL string

// Schema returns the embedded DDL for the outbox table + indexes.
// Migration runners (golang-migrate, goose, sqlx-migrate, …) call
// this to install the schema. Idempotent — every statement uses
// IF NOT EXISTS.
func Schema() string { return schemaSQL }

// Stable error Code constants. Use to switch on *errs.Error.Code in
// callers / dashboards.
const (
	// CodeEnqueueFailed — Enqueue's INSERT into outbox failed.
	// Surfaces as the wrapped pgx error; the surrounding transaction
	// MUST roll back to keep business state and outbox consistent.
	CodeEnqueueFailed = "outbox_enqueue_failed"

	// CodeMissingFields — Enqueue refused an Event with empty
	// EventType (the one unconditionally-required field). Other
	// fields default to zero values where it makes sense.
	CodeMissingFields = "outbox_missing_fields"

	// CodeMarshalHeaders — Headers map could not be JSON-encoded.
	// Practically only fires on cyclic / nan / inf header values —
	// JSON-encodable map[string][]string never trips this. Returned
	// from Enqueue, not Worker.
	CodeMarshalHeaders = "outbox_marshal_headers"
)

// Event is the row shape persisted to the outbox table. The Worker
// hands a fully-hydrated Event to its PublishFn after a successful
// SELECT — fields are read back exactly as Enqueue stored them.
//
// Payload is opaque []byte: callers encode their typed payload
// before Enqueue (JSON, protobuf, …) and decode in the PublishFn or
// downstream subscriber. The outbox doesn't care about the encoding
// — it's a transport, not a schema registry.
//
// Headers is a key→values multi-map matching NATS-style header
// shape. Persisted as JSONB so the row is self-describing in pg
// admin tooling.
type Event struct {
	// ID is the event's primary key. Empty on input to Enqueue (the
	// DB defaults a fresh UUID); populated on output from the
	// Worker's SELECT.
	ID uuid.UUID

	// AggregateType + AggregateID identify the entity the event is
	// about (e.g. "link", "abc123"). Optional, but recommended —
	// downstream tooling can correlate events back to their source
	// without parsing the payload.
	AggregateType string
	AggregateID   string

	// EventType is the bus-side routing key (e.g.
	// "urlshort.link.created"). Required. The Worker's PublishFn
	// usually maps this onto a publisher name / subject.
	EventType string

	// Payload is the wire-format event body. Caller encodes; Worker
	// passes through unchanged.
	Payload []byte

	// Headers are optional per-event metadata propagated to the bus.
	// Schemas like W3C TraceContext travel here.
	Headers map[string][]string

	// CreatedAt / PublishedAt are managed by the table — Enqueue
	// leaves CreatedAt zero so the DB default fires; the Worker
	// hydrates both on read.
	CreatedAt   time.Time
	PublishedAt time.Time

	// Attempts is the count of times PublishFn ran AND failed for
	// this event. Bumped by the Worker before each retry.
	Attempts int

	// LastError is the formatted error string from the most recent
	// PublishFn failure. Empty on first attempt / after a successful
	// publish (the row leaves the unpublished set entirely on
	// success).
	LastError string
}

// Enqueue writes an Event into the outbox table inside the caller's
// db.Querier (typically a *db.Tx). The Event's ID, CreatedAt, and
// Attempts fields are managed by the table — caller leaves them
// zero. AggregateType / AggregateID / Headers are optional.
//
// Returns *errs.Error{Code: CodeMissingFields} if EventType is
// empty (the one unconditionally-required field). Other validation
// errors (Headers not JSON-encodable, INSERT failure) surface with
// the matching Code constant.
//
// Idempotency: Enqueue inserts a fresh row every call. To dedupe
// upstream, set ID yourself before Enqueue and rely on the PRIMARY
// KEY (callers that want this semantic add a UNIQUE constraint on
// (aggregate_type, aggregate_id, event_type) via their own
// migration).
func Enqueue(ctx context.Context, q db.Querier, e Event) error {
	if e.EventType == "" {
		return errs.Validation(CodeMissingFields, "outbox: Event.EventType is required")
	}
	// Snapshot the current OTel TraceContext into the row so the
	// downstream Worker dispatch preserves the originating trace
	// across the async boundary. The headers (now carrying
	// traceparent / tracestate when a span is active in ctx) are
	// persisted as JSONB and replayed verbatim on publish — see
	// publishBytes's InjectTraceContext "skip if already present"
	// branch in clients/nats/propagation.go.
	e.Headers = natsclient.InjectTraceContext(ctx, e.Headers)
	var headersJSON []byte
	if len(e.Headers) > 0 {
		var err error
		headersJSON, err = json.Marshal(e.Headers)
		if err != nil {
			return errs.Wrap(err, errs.KindValidation, CodeMarshalHeaders,
				"outbox: encode headers")
		}
	}
	// pg_notify('outbox_new', '') fires at COMMIT time (per Postgres
	// semantics — NOTIFY is buffered to commit) so Worker LISTENers
	// wake up immediately when the surrounding transaction is durable.
	// Payload is empty: listeners only need the "something happened"
	// signal, the lookup happens via the indexed SELECT.
	const sql = `
		WITH ins AS (
			INSERT INTO outbox (aggregate_type, aggregate_id, event_type, payload, headers)
			VALUES ($1, $2, $3, $4, $5)
			RETURNING 1
		)
		SELECT pg_notify('` + NotifyChannel + `', '') FROM ins
	`
	if _, err := q.Exec(ctx, sql,
		e.AggregateType, e.AggregateID, e.EventType, e.Payload, headersJSON,
	); err != nil {
		return errs.Wrap(err, errs.KindInternal, CodeEnqueueFailed,
			"outbox: insert event")
	}
	return nil
}

// NotifyChannel is the Postgres LISTEN channel the Worker subscribes
// to for low-latency wake-up. Enqueue's INSERT fires
// pg_notify(NotifyChannel, ”) so Worker.Start's listen goroutine
// signals the drain loop within milliseconds of COMMIT instead of
// waiting for the next polling tick.
const NotifyChannel = "outbox_new"
