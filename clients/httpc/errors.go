package httpc

// Error codes returned in *errs.Error.Code from New / NewTransport
// config validation. Network errors and exhausted retries pass through
// as stdlib errors (not *errs.Error) — see the design spec.
const (
	CodeInvalidTimeout    = "httpc_invalid_timeout"
	CodeInvalidMaxRetries = "httpc_invalid_max_retries"
	CodeInvalidBackoff    = "httpc_invalid_backoff"

	// CodeCircuitOpen is the *errs.Error Code returned when a request
	// is short-circuited by an open circuit breaker installed via
	// [WithBreaker]. The Cause is [breaker.ErrOpen], so
	// errors.Is(err, breaker.ErrOpen) holds; errs.HTTP(err) yields
	// 503 with this code.
	CodeCircuitOpen = "httpc_circuit_open"
)
