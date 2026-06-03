package batch

import "errors"

// Stable error Codes returned by [New] via *errs.Error.
const (
	// CodeMissingHandlerFn — Config.HandlerFn was nil at New.
	CodeMissingHandlerFn = "batch_missing_handler_fn"

	// CodeInvalidBatchSize — Config.BatchSize was <= 0 at New.
	// BatchSize is required; the size trigger is the primary flush
	// driver.
	CodeInvalidBatchSize = "batch_invalid_batch_size"

	// CodeInvalidConfig — generic Config validation failure
	// (MaxPending negative, MaxRetries negative, etc.).
	CodeInvalidConfig = "batch_invalid_config"

	// CodePendingFull — backpressure signal raised by Submit /
	// TrySubmit when Config.MaxPending > 0 and the buffer is at the
	// cap. The wire shape returned to callers is the plain
	// [ErrPendingFull] sentinel (no *errs.Error wrap) so a tight
	// Submit loop can errors.Is-check without an allocation.
	CodePendingFull = "batch_pending_full"
)

// ErrPendingFull is returned by Submit / TrySubmit when Config.MaxPending
// > 0 and the in-memory buffer is at the cap. Use errors.Is to detect.
var ErrPendingFull = errors.New("batch: pending buffer full")
