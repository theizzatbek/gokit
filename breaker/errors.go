package breaker

import "errors"

// ErrOpen is returned by [Breaker.Execute] (and recognised by adapters
// such as clients/httpc) when the circuit is open and the call was
// short-circuited without touching the wire.
//
// Adapters typically wrap ErrOpen into their own typed error (for
// example, clients/httpc wraps it into *errs.Error with Code
// "httpc_circuit_open"). errors.Is(err, breaker.ErrOpen) keeps working
// after wrapping.
var ErrOpen = errors.New("breaker: circuit open")

// Stable error Codes returned by [New] via the local [Error] type when
// [Config] validation fails. The package intentionally does NOT depend
// on errs/; adapters wishing to surface a *errs.Error map these codes
// at the boundary.
const (
	// CodeInvalidName — Config.Name was empty.
	CodeInvalidName = "breaker_invalid_name"

	// CodeInvalidFailureThreshold — Config.FailureThreshold was < 1.
	CodeInvalidFailureThreshold = "breaker_invalid_failure_threshold"

	// CodeInvalidMinimumRequests — Config.MinimumRequests was < 1 or
	// less than FailureThreshold (the floor must not exceed the trip
	// condition or the breaker can never open).
	CodeInvalidMinimumRequests = "breaker_invalid_minimum_requests"

	// CodeInvalidWindow — WindowDuration <= 0 or WindowSize < 1, or
	// WindowDuration is not divisible into WindowSize buckets cleanly
	// (we require an integer number of nanoseconds per bucket).
	CodeInvalidWindow = "breaker_invalid_window"

	// CodeInvalidOpenInterval — Config.OpenInterval <= 0.
	CodeInvalidOpenInterval = "breaker_invalid_open_interval"

	// CodeInvalidHalfOpenMaxProbes — Config.HalfOpenMaxProbes < 1.
	CodeInvalidHalfOpenMaxProbes = "breaker_invalid_half_open_max_probes"
)

// Error is the validation error type returned by [New]. It carries a
// stable Code (one of the Code* constants above) plus a human-readable
// Message. Callers wishing to lift it into a richer error shape
// (e.g. *errs.Error) can read Code at the boundary.
type Error struct {
	Code    string
	Message string
}

// Error implements the error interface.
func (e *Error) Error() string { return e.Message }

// newError is the internal constructor used by Config.validate.
func newError(code, msg string) *Error { return &Error{Code: code, Message: msg} }
