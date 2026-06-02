package inboxnats

import (
	"context"

	natsclient "github.com/theizzatbek/gokit/clients/nats"
	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/db/inbox"
	xerrs "github.com/theizzatbek/gokit/errs"
)

// NatsMsgIDHeader is the standard JetStream de-duplication header.
// Publishers set it via `nats.MsgId(...)` (or natsmap's auto-injected
// X-Request-ID-derived id, when configured). The default
// [EventIDFn] reads this exact key.
const NatsMsgIDHeader = "Nats-Msg-Id"

// CodeMissingMessageID is the *errs.Error Code returned by a wrapped
// handler when the message lacks an id under the configured header.
// natsmap propagates the error to the runtime as a Nak so the
// publisher misconfig surfaces in alerts rather than silently
// disabling dedup.
const CodeMissingMessageID = "inboxnats_missing_message_id"

// EventIDFn extracts the event id from a NATS message's metadata.
// The default implementation reads headers[NatsMsgIDHeader][0];
// override via [WithEventIDFn] when the publisher uses a different
// convention (e.g. a typed body field — Sequence + Subject can stand
// in for a publisher that does not stamp Nats-Msg-Id).
type EventIDFn func(headers map[string][]string, subject string, sequence uint64) string

// Option tunes [Wrap].
type Option func(*options)

type options struct {
	inbox     *inbox.Inbox // nil = package-level inbox.Process
	eventIDFn EventIDFn
}

// WithInbox passes a captured *inbox.Inbox handle that carries
// logger + metrics. nil falls back to the package-level
// [inbox.Process] (no observability hooks fire).
func WithInbox(in *inbox.Inbox) Option {
	return func(o *options) { o.inbox = in }
}

// WithEventIDFn overrides how the event id is extracted from a
// natsclient.Msg. The default uses [NatsMsgIDHeader].
func WithEventIDFn(fn EventIDFn) Option {
	return func(o *options) { o.eventIDFn = fn }
}

// DefaultEventIDFn reads NatsMsgIDHeader (`Nats-Msg-Id`) from headers,
// returning "" when absent. Exposed so callers can compose it into a
// custom resolver that falls back on Sequence or a payload field.
func DefaultEventIDFn(headers map[string][]string, _ string, _ uint64) string {
	if v, ok := headers[NatsMsgIDHeader]; ok && len(v) > 0 {
		return v[0]
	}
	return ""
}

// Wrap returns a natsmap-compatible handler that dedups through the
// inbox table before invoking fn. fn receives the Tx that owns the
// inbox row insert — its writes commit atomically with the row.
//
// The wrapped handler short-circuits with *errs.Error{Code:
// [CodeMissingMessageID]} when the event id resolver yields "". The
// inbox-layer Process error is propagated unchanged on
// [inbox.OutcomeProcessed] failures; [inbox.OutcomeDuplicate]
// returns nil (the wrapper-supplied fn did NOT run, but the
// redelivery is acked because the row already exists).
func Wrap[T any](
	consumer string,
	d *db.DB,
	fn func(ctx context.Context, tx *db.Tx, m natsclient.Msg[T]) error,
	opts ...Option,
) func(ctx context.Context, m natsclient.Msg[T]) error {
	o := &options{eventIDFn: DefaultEventIDFn}
	for _, opt := range opts {
		opt(o)
	}
	return func(ctx context.Context, m natsclient.Msg[T]) error {
		eventID := o.eventIDFn(m.Headers, m.Subject, m.Sequence)
		if eventID == "" {
			return xerrs.Validationf(CodeMissingMessageID,
				"inboxnats: missing %q header on subject %q (publisher must set nats.MsgId or override via WithEventIDFn)",
				NatsMsgIDHeader, m.Subject)
		}
		key := inbox.Key{Consumer: consumer, EventID: eventID}
		// Method-value on a typed nil would panic; pick the package-
		// level function when no Inbox handle was passed.
		process := inbox.Process
		if o.inbox != nil {
			process = o.inbox.Process
		}
		_, err := process(ctx, d, key, func(tx *db.Tx) error {
			return fn(ctx, tx, m)
		})
		return err
	}
}
