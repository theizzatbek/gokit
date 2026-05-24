// Package errs defines typed domain errors for the fibermap kit.
//
// Every kit subpackage (db, auth, jobs, clients) returns *Error for known
// runtime conditions. Each *Error carries a Kind (closed enum), a stable
// machine-readable Code, a human-readable Message, optional Details for
// field-level failures, and the wrapped Cause.
//
// Use the per-Kind constructors (NotFound, Validation, ...) or their
// Sprintf-flavored siblings (NotFoundf, ...). Use Wrap/Wrapf when an
// existing error needs to be lifted to a kit-level Error.
//
// HTTP transport lives in the http.go helper: HTTP(err) returns the status
// code and the JSON-ready Response body. Wire it into Fiber via
// fibermap.ErrorHandler(logger) (root package) — that helper also auto-logs
// 5xx kinds via slog.
//
// errs imports stdlib only. Validator-error conversion lives in the
// errs/errsval subpackage.
package errs
