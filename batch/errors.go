package batch

// Stable error Codes returned by [New] via *errs.Error.
const (
	// CodeMissingHandlerFn — Config.HandlerFn was nil at New.
	CodeMissingHandlerFn = "batch_missing_handler_fn"

	// CodeInvalidBatchSize — Config.BatchSize was <= 0 at New.
	// BatchSize is required; the size trigger is the primary flush
	// driver. Use a large BatchSize + small Interval for "almost
	// always size-trigger" deployments; use a small BatchSize +
	// large Interval for "drain-on-tick" pipelines.
	CodeInvalidBatchSize = "batch_invalid_batch_size"
)
