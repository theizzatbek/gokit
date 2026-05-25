package httpc

// Error codes returned in *errs.Error.Code from New / NewTransport
// config validation. Network errors and exhausted retries pass through
// as stdlib errors (not *errs.Error) — see the design spec.
const (
	CodeInvalidTimeout    = "httpc_invalid_timeout"
	CodeInvalidMaxRetries = "httpc_invalid_max_retries"
	CodeInvalidBackoff    = "httpc_invalid_backoff"
)
