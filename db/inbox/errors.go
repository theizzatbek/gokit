package inbox

// Stable error Codes returned in *errs.Error.Code from Process,
// NewRetentionWorker, and the retention tick.
const (
	// CodeMissingConsumer — Key.Consumer was empty at Process.
	CodeMissingConsumer = "inbox_missing_consumer"

	// CodeMissingEventID — Key.EventID was empty at Process.
	CodeMissingEventID = "inbox_missing_event_id"

	// CodeTxFailed — the inbox transaction (INSERT + caller fn +
	// commit) returned an error. Wraps the underlying cause; common
	// cases are pgx connection loss and "relation inbox does not
	// exist" when Schema() was not applied.
	CodeTxFailed = "inbox_tx_failed"

	// CodeInvalidRetentionTTL — RetentionConfig.TTL was zero or
	// negative at NewRetentionWorker. Unlike outbox's worker, inbox
	// does not default TTL — there is no sane "keep forever" or
	// "keep an hour" default for receipts.
	CodeInvalidRetentionTTL = "inbox_invalid_retention_ttl"

	// CodeInvalidRetentionInterval — RetentionConfig.Interval was
	// zero or negative.
	CodeInvalidRetentionInterval = "inbox_invalid_retention_interval"

	// CodeRetentionTickFailed — a retention DELETE failed mid-run.
	// The worker logs and continues on the next interval; this code
	// surfaces only when a caller invokes RetentionWorker.Tick()
	// directly (e.g. from a test or one-shot prune).
	CodeRetentionTickFailed = "inbox_retention_tick_failed"
)
