// Package errs defines typed domain errors and their HTTP mapping.
package errs

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
