// Package bind provides typed request parse + validation helpers for
// fibermap (or any Fiber) handlers.
//
// Four symmetric one-liners are exposed:
//
//   - Body[T]   — parse JSON/form body via Fiber's BodyParser
//   - Query[T]  — parse query string via Fiber's QueryParser
//   - Params[T] — parse route params (:id, :slug, …) via Fiber's ParamsParser
//   - Header[T] — parse request headers via Fiber's ReqHeaderParser
//
// Each accepts a Validator (typically *validator.Validate from
// github.com/go-playground/validator/v10) — pass nil to skip validation.
// The package deliberately does NOT depend on a specific validator
// implementation; it talks to validators through a one-method Validator
// interface. Any type with a `Struct(any) error` method works.
//
// Typical use with go-playground/validator:
//
//	import (
//	    "github.com/go-playground/validator/v10"
//	    "github.com/theizzatbek/fibermap/bind"
//	)
//
//	var v = validator.New()
//
//	type CreateTaskReq struct {
//	    Title string `json:"title" validate:"required,min=1,max=200"`
//	}
//
//	func (h *H) Create(c *fibermap.Context[AppCtx]) error {
//	    req, err := bind.Body[CreateTaskReq](c.Ctx, v)
//	    if err != nil {
//	        return c.Status(400).JSON(fiber.Map{"error": err.Error()})
//	    }
//	    // req is populated and validated.
//	    ...
//	}
//
// Each helper returns its own sentinel errors so callers can branch:
//
//   - ErrParseBody    / ErrValidateBody
//   - ErrParseQuery   / ErrValidateQuery
//   - ErrParseParams  / ErrValidateParams
//   - ErrParseHeader  / ErrValidateHeader
package bind

import (
	"errors"
	"fmt"
)

// Validator is the minimal contract a request validator must satisfy.
// *validator.Validate from go-playground/validator/v10 satisfies it directly.
type Validator interface {
	Struct(s any) error
}

// BodyParser is the minimal contract for Body[T]. Both *fiber.Ctx and (by
// embedding) *fibermap.Context[T] satisfy it.
type BodyParser interface {
	BodyParser(out any) error
}

// QueryParser is the minimal contract for Query[T]. Both *fiber.Ctx and (by
// embedding) *fibermap.Context[T] satisfy it.
type QueryParser interface {
	QueryParser(out any) error
}

// ParamsParser is the minimal contract for Params[T]. Both *fiber.Ctx and
// (by embedding) *fibermap.Context[T] satisfy it.
type ParamsParser interface {
	ParamsParser(out any) error
}

// ReqHeaderParser is the minimal contract for Header[T]. Both *fiber.Ctx
// and (by embedding) *fibermap.Context[T] satisfy it.
type ReqHeaderParser interface {
	ReqHeaderParser(out any) error
}

// ErrParseBody wraps a body-parsing failure.
var ErrParseBody = errors.New("bind: parse body")

// ErrValidateBody wraps a body-validation failure.
var ErrValidateBody = errors.New("bind: validate body")

// ErrParseQuery wraps a query-parsing failure.
var ErrParseQuery = errors.New("bind: parse query")

// ErrValidateQuery wraps a query-validation failure.
var ErrValidateQuery = errors.New("bind: validate query")

// ErrParseParams wraps a route-param parsing failure.
var ErrParseParams = errors.New("bind: parse params")

// ErrValidateParams wraps a route-param validation failure.
var ErrValidateParams = errors.New("bind: validate params")

// ErrParseHeader wraps a request-header parsing failure.
var ErrParseHeader = errors.New("bind: parse header")

// ErrValidateHeader wraps a request-header validation failure.
var ErrValidateHeader = errors.New("bind: validate header")

// Body parses the request body into a fresh T and runs the validator
// over the result. Returns the populated T on success.
//
// On parse failure the returned error wraps ErrParseBody; on
// validation failure it wraps ErrValidateBody. Callers can branch
// with errors.Is to pick the right HTTP status (typically 400 for
// both, but you may want different bodies).
func Body[T any](c BodyParser, v Validator) (T, error) {
	var body T
	if err := c.BodyParser(&body); err != nil {
		return body, fmt.Errorf("%w: %v", ErrParseBody, err)
	}
	if v != nil {
		if err := v.Struct(&body); err != nil {
			return body, fmt.Errorf("%w: %v", ErrValidateBody, err)
		}
	}
	return body, nil
}

// Query parses the request query string into a fresh T and runs the
// validator over the result. Returns the populated T on success.
//
// On parse failure the returned error wraps ErrParseQuery; on
// validation failure it wraps ErrValidateQuery.
func Query[T any](c QueryParser, v Validator) (T, error) {
	var q T
	if err := c.QueryParser(&q); err != nil {
		return q, fmt.Errorf("%w: %v", ErrParseQuery, err)
	}
	if v != nil {
		if err := v.Struct(&q); err != nil {
			return q, fmt.Errorf("%w: %v", ErrValidateQuery, err)
		}
	}
	return q, nil
}

// Params parses route params (:id, :slug, …) into a fresh T and runs
// the validator over the result. Returns the populated T on success.
//
// On parse failure the returned error wraps ErrParseParams; on
// validation failure it wraps ErrValidateParams.
func Params[T any](c ParamsParser, v Validator) (T, error) {
	var p T
	if err := c.ParamsParser(&p); err != nil {
		return p, fmt.Errorf("%w: %v", ErrParseParams, err)
	}
	if v != nil {
		if err := v.Struct(&p); err != nil {
			return p, fmt.Errorf("%w: %v", ErrValidateParams, err)
		}
	}
	return p, nil
}

// Header parses request headers into a fresh T and runs the validator
// over the result. Struct fields use the `reqHeader:"X-Name"` tag (the
// convention Fiber's ReqHeaderParser expects).
//
// Common targets: `Authorization`, `X-Idempotency-Key`, `Accept`,
// `Accept-Language`, custom tenant headers.
//
// On parse failure the returned error wraps ErrParseHeader; on
// validation failure it wraps ErrValidateHeader.
func Header[T any](c ReqHeaderParser, v Validator) (T, error) {
	var h T
	if err := c.ReqHeaderParser(&h); err != nil {
		return h, fmt.Errorf("%w: %v", ErrParseHeader, err)
	}
	if v != nil {
		if err := v.Struct(&h); err != nil {
			return h, fmt.Errorf("%w: %v", ErrValidateHeader, err)
		}
	}
	return h, nil
}
