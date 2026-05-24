// Package errs defines typed domain errors and their HTTP mapping.
package errs

import "fmt"

// Kind classifies an error for HTTP mapping and structured logging.
// It is a closed enum; adding a new Kind is a breaking change in 0.x.
type Kind int

const (
	KindUnknown Kind = iota
	KindNotFound
	KindAlreadyExists
	KindConflict
	KindValidation
	KindUnauthorized
	KindPermission
	KindRateLimited
	KindUnavailable
	KindTimeout
	KindInternal
)

var kindNames = map[Kind]string{
	KindUnknown:       "unknown",
	KindNotFound:      "not_found",
	KindAlreadyExists: "already_exists",
	KindConflict:      "conflict",
	KindValidation:    "validation",
	KindUnauthorized:  "unauthorized",
	KindPermission:    "permission",
	KindRateLimited:   "rate_limited",
	KindUnavailable:   "unavailable",
	KindTimeout:       "timeout",
	KindInternal:      "internal",
}

func (k Kind) String() string {
	if name, ok := kindNames[k]; ok {
		return name
	}
	return "unknown"
}

// FieldError describes a single field-level failure (typically from input validation).
type FieldError struct {
	Field   string `json:"field"`
	Rule    string `json:"rule"`
	Param   string `json:"param,omitempty"`
	Message string `json:"message"`
}

// Error is the typed error every kit subpackage returns for known failure conditions.
type Error struct {
	Kind    Kind
	Code    string
	Message string
	Details []FieldError
	Cause   error
}

// Error renders the error for logs and tests. Wire shape lives in http.go.
func (e *Error) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s: %s: %s", e.Kind.String(), e.Code, e.Message, e.Cause.Error())
	}
	return fmt.Sprintf("%s: %s: %s", e.Kind.String(), e.Code, e.Message)
}

// Unwrap returns the wrapped cause, enabling errors.Is/As to walk the chain.
func (e *Error) Unwrap() error { return e.Cause }

// NotFound returns a *Error of KindNotFound.
func NotFound(code, msg string) *Error {
	return &Error{Kind: KindNotFound, Code: code, Message: msg}
}

// AlreadyExists returns a *Error of KindAlreadyExists.
func AlreadyExists(code, msg string) *Error {
	return &Error{Kind: KindAlreadyExists, Code: code, Message: msg}
}

// Conflict returns a *Error of KindConflict.
func Conflict(code, msg string) *Error {
	return &Error{Kind: KindConflict, Code: code, Message: msg}
}

// Validation returns a *Error of KindValidation. Inline details are accepted as the
// common case (caller built them by hand). For larger lists use WithDetails.
func Validation(code, msg string, details ...FieldError) *Error {
	return &Error{Kind: KindValidation, Code: code, Message: msg, Details: details}
}

// Unauthorized returns a *Error of KindUnauthorized.
func Unauthorized(code, msg string) *Error {
	return &Error{Kind: KindUnauthorized, Code: code, Message: msg}
}

// Permission returns a *Error of KindPermission.
func Permission(code, msg string) *Error {
	return &Error{Kind: KindPermission, Code: code, Message: msg}
}

// RateLimited returns a *Error of KindRateLimited.
func RateLimited(code, msg string) *Error {
	return &Error{Kind: KindRateLimited, Code: code, Message: msg}
}

// Unavailable returns a *Error of KindUnavailable.
func Unavailable(code, msg string) *Error {
	return &Error{Kind: KindUnavailable, Code: code, Message: msg}
}

// Timeout returns a *Error of KindTimeout.
func Timeout(code, msg string) *Error {
	return &Error{Kind: KindTimeout, Code: code, Message: msg}
}

// Internal returns a *Error of KindInternal.
func Internal(code, msg string) *Error {
	return &Error{Kind: KindInternal, Code: code, Message: msg}
}

// NotFoundf is the Sprintf-flavored NotFound.
func NotFoundf(code, format string, args ...any) *Error {
	return NotFound(code, fmt.Sprintf(format, args...))
}

// AlreadyExistsf is the Sprintf-flavored AlreadyExists.
func AlreadyExistsf(code, format string, args ...any) *Error {
	return AlreadyExists(code, fmt.Sprintf(format, args...))
}

// Conflictf is the Sprintf-flavored Conflict.
func Conflictf(code, format string, args ...any) *Error {
	return Conflict(code, fmt.Sprintf(format, args...))
}

// Validationf is the Sprintf-flavored Validation. Details are added later via WithDetails.
func Validationf(code, format string, args ...any) *Error {
	return Validation(code, fmt.Sprintf(format, args...))
}

// Unauthorizedf is the Sprintf-flavored Unauthorized.
func Unauthorizedf(code, format string, args ...any) *Error {
	return Unauthorized(code, fmt.Sprintf(format, args...))
}

// Permissionf is the Sprintf-flavored Permission.
func Permissionf(code, format string, args ...any) *Error {
	return Permission(code, fmt.Sprintf(format, args...))
}

// RateLimitedf is the Sprintf-flavored RateLimited.
func RateLimitedf(code, format string, args ...any) *Error {
	return RateLimited(code, fmt.Sprintf(format, args...))
}

// Unavailablef is the Sprintf-flavored Unavailable.
func Unavailablef(code, format string, args ...any) *Error {
	return Unavailable(code, fmt.Sprintf(format, args...))
}

// Timeoutf is the Sprintf-flavored Timeout.
func Timeoutf(code, format string, args ...any) *Error {
	return Timeout(code, fmt.Sprintf(format, args...))
}

// Internalf is the Sprintf-flavored Internal.
func Internalf(code, format string, args ...any) *Error {
	return Internal(code, fmt.Sprintf(format, args...))
}

// WithDetails appends details to e and returns e for chaining.
// Useful for the Sprintf-flavored constructors which take no details argument.
func (e *Error) WithDetails(details ...FieldError) *Error {
	e.Details = append(e.Details, details...)
	return e
}
