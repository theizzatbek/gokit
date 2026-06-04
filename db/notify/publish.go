package notify

import (
	"context"
	"encoding/json"

	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/errs"
)

// Stable Code constants for the publisher path.
const (
	// CodeInvalidChannel — Publish / PublishRaw received an empty
	// or unsafe channel name (anything outside the [A-Za-z0-9_]
	// alphabet that LISTEN accepts unquoted).
	CodeInvalidChannel = "notify_invalid_channel"

	// CodeEncodeFailed — Publish could not JSON-encode the payload.
	CodeEncodeFailed = "notify_encode_failed"

	// CodePublishFailed — pg_notify SQL call errored.
	CodePublishFailed = "notify_publish_failed"
)

// Publish emits one pg_notify event on channel with payload encoded
// as JSON. Symmetric to [Notifier] on the publisher side — every
// in-process subscriber receives the matching JSON-decoded Notification.
//
// Use [PublishRaw] when payload is already a string (e.g. a small
// hint key); Publish is the typed JSON wrapper for richer events.
//
// Channel name validation matches [Notifier.registerChannels] — only
// letters, digits, underscores are accepted, leading character must
// be a letter or underscore. Postgres allows these as unquoted
// identifiers, so the kit doesn't drag in a full quoter.
//
//	type CacheBust struct {
//	    Tenant string `json:"tenant"`
//	    Key    string `json:"key"`
//	}
//	err := notify.Publish(ctx, svc.DB, "cache_bust",
//	    CacheBust{Tenant: "acme", Key: "config:flags"})
//
// Errors:
//   - notify_invalid_channel — channel is empty or has unsafe chars
//   - notify_encode_failed   — JSON encode of payload errored
//   - notify_publish_failed  — underlying pg_notify call errored
//
// Publish accepts any [db.Querier] so the call can run inside a
// caller's transaction — pg_notify is buffered to COMMIT, so the
// subscriber wakes up only after the surrounding work is durable.
func Publish[T any](ctx context.Context, q db.Querier, channel string, payload T) error {
	if !safeIdent(channel) {
		return errs.Validationf(CodeInvalidChannel,
			"notify: unsafe channel name %q", channel)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return errs.Wrap(err, errs.KindValidation, CodeEncodeFailed,
			"notify: encode payload")
	}
	return publish(ctx, q, channel, string(raw))
}

// PublishRaw is the lower-level companion to [Publish] — call when
// the payload is already a string and no JSON encoding is needed.
// Useful for "something happened in <channel>" wake-up signals where
// the subscriber re-queries the source of truth (the kit's outbox
// uses this pattern).
//
// Empty payload is accepted (pg_notify supports empty notification
// strings). Channel validation matches [Publish].
func PublishRaw(ctx context.Context, q db.Querier, channel, payload string) error {
	if !safeIdent(channel) {
		return errs.Validationf(CodeInvalidChannel,
			"notify: unsafe channel name %q", channel)
	}
	return publish(ctx, q, channel, payload)
}

// publish issues the actual SELECT pg_notify call. Shared by Publish
// + PublishRaw; both delegate after their own validation step.
func publish(ctx context.Context, q db.Querier, channel, payload string) error {
	if _, err := q.Exec(ctx, "SELECT pg_notify($1, $2)", channel, payload); err != nil {
		return errs.Wrap(err, errs.KindInternal, CodePublishFailed,
			"notify: pg_notify")
	}
	return nil
}
