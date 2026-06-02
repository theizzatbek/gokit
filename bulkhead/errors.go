package bulkhead

import "errors"

// ErrBulkheadFull is returned by [Bulkhead.Acquire] when both the
// in-flight semaphore and the wait queue are saturated. Adapters
// typically wrap it into a *errs.Error with their own stable Code
// (e.g. clients/httpc uses "httpc_bulkhead_full"); errors.Is keeps
// working after wrapping.
var ErrBulkheadFull = errors.New("bulkhead: full")

// ErrQueueTimeout is returned by [Bulkhead.Acquire] when [Config.QueueTimeout]
// fires before a slot becomes available. Distinct from context.DeadlineExceeded
// so dashboards can tell apart "bulkhead-imposed wait limit" from "caller's
// own deadline ran out."
var ErrQueueTimeout = errors.New("bulkhead: queue wait timeout")

// Stable error Codes returned by [New] via the local [Error] type when
// [Config] validation fails. The package intentionally does NOT depend
// on errs/; adapters wishing to surface a *errs.Error map these codes
// at the boundary.
const (
	// CodeInvalidName — Config.Name was empty.
	CodeInvalidName = "bulkhead_invalid_name"

	// CodeInvalidMaxConcurrent — Config.MaxConcurrent < 1.
	CodeInvalidMaxConcurrent = "bulkhead_invalid_max_concurrent"

	// CodeInvalidMaxQueue — Config.MaxQueue < 0. Unlimited queue is
	// deliberately not supported — that is the failure mode bulkhead
	// exists to prevent.
	CodeInvalidMaxQueue = "bulkhead_invalid_max_queue"

	// CodeInvalidQueueTimeout — Config.QueueTimeout < 0.
	CodeInvalidQueueTimeout = "bulkhead_invalid_queue_timeout"

	// CodeInvalidAdaptiveConfig — WithAdaptive's AdaptiveConfig
	// failed validation (missing Controller; InitialCap or bounds
	// out of range; Config.MaxConcurrent set alongside WithAdaptive).
	CodeInvalidAdaptiveConfig = "bulkhead_invalid_adaptive_config"
)

// Error is the validation error type returned by [New]. It carries a
// stable Code (one of the Code* constants above) plus a human-readable
// Message. Adapters wishing to lift it into a richer error shape
// (e.g. *errs.Error) read Code at the boundary.
type Error struct {
	Code    string
	Message string
}

// Error implements the error interface.
func (e *Error) Error() string { return e.Message }

// newError is the internal constructor used by Config.validate.
func newError(code, msg string) *Error { return &Error{Code: code, Message: msg} }
