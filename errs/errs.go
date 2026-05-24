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
