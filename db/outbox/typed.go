package outbox

import (
	"context"
	"encoding/json"

	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/errs"
)

// EnqueueOption tunes [EnqueueTyped] beyond what eventType + payload
// already supply.
type EnqueueOption func(*Event)

// WithAggregate stamps aggregate_type + aggregate_id on the event.
// Optional — events without an aggregate (e.g. system-wide
// notifications) leave both fields empty.
//
//	outbox.EnqueueTyped(ctx, tx, "urlshort.link.created", payload,
//	    outbox.WithAggregate("link", link.Code))
func WithAggregate(aggregateType, aggregateID string) EnqueueOption {
	return func(e *Event) {
		e.AggregateType = aggregateType
		e.AggregateID = aggregateID
	}
}

// WithEventHeaders sets the headers map on the event. The headers
// travel verbatim onto the bus and are also persisted as JSONB
// alongside the event so a Worker dispatching the row later sees
// the same shape the originating call did.
//
// Trace context is auto-injected by [Enqueue] — callers do NOT
// need to set traceparent themselves.
func WithEventHeaders(headers map[string][]string) EnqueueOption {
	return func(e *Event) { e.Headers = headers }
}

// EnqueueTyped is the ergonomic typed wrapper around [Enqueue]:
// payload is JSON-encoded before persistence, EventType is the bus
// subject. Replaces the json.Marshal + outbox.Event{Payload: ...}
// ceremony that v1 callers had to repeat at every Enqueue site.
//
//	type LinkCreated struct { ... }
//	err := outbox.EnqueueTyped(ctx, tx, "urlshort.link.created",
//	    LinkCreated{...},
//	    outbox.WithAggregate("link", link.Code))
//
// JSON encode failure surfaces as *errs.Error{Code: CodeEncodeFailed}.
// Underlying Enqueue errors flow through unchanged.
func EnqueueTyped[T any](ctx context.Context, q db.Querier, eventType string, payload T, opts ...EnqueueOption) error {
	if eventType == "" {
		return errs.Validation(CodeMissingFields, "outbox: eventType is required")
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return errs.Wrap(err, errs.KindValidation, CodeEncodeFailed,
			"outbox: encode typed payload")
	}
	e := Event{EventType: eventType, Payload: raw}
	for _, opt := range opts {
		opt(&e)
	}
	return Enqueue(ctx, q, e)
}

// CodeEncodeFailed — EnqueueTyped's JSON marshal failed. Rare in
// practice — only fires on cyclic structs / NaN floats / channels.
const CodeEncodeFailed = "outbox_encode_failed"
