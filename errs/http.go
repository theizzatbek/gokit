package errs

import "errors"

// Response is the wire shape returned to clients for any error.
type Response struct {
	Code    string       `json:"code"`
	Message string       `json:"message"`
	Details []FieldError `json:"details,omitempty"`
}

var kindStatus = map[Kind]int{
	KindNotFound:      404,
	KindAlreadyExists: 409,
	KindConflict:      409,
	KindValidation:    400,
	KindUnauthorized:  401,
	KindPermission:    403,
	KindRateLimited:   429,
	KindUnavailable:   503,
	KindTimeout:       504,
	KindInternal:      500,
}

// HTTP returns (status, body) for err.
//   - if err is *Error (incl. via errors.As): status from kind table, body from fields.
//   - if err is nil: returns (200, Response{}). Documented but not expected to be called.
//   - any other non-nil error: returns (500, Response{Code: "internal_error", Message: "internal server error"}).
//
// HTTP does NOT special-case *fiber.Error — that lives in the root package's ErrorHandler.
func HTTP(err error) (int, Response) {
	if err == nil {
		return 200, Response{}
	}
	var e *Error
	if errors.As(err, &e) {
		status, ok := kindStatus[e.Kind]
		if !ok {
			status = 500
		}
		return status, Response{Code: e.Code, Message: e.Message, Details: e.Details}
	}
	return 500, Response{Code: "internal_error", Message: "internal server error"}
}
