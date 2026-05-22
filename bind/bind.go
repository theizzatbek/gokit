// Package bind provides typed request-body parsing + validation
// helpers for fibermap (or any Fiber) handlers.
//
// The package deliberately does NOT depend on a specific validator
// implementation — it talks to validators through a one-method
// Validator interface. The popular `*validator.Validate` from
// github.com/go-playground/validator/v10 satisfies the interface
// as-is, but any type with a `Struct(any) error` method works.
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
// Bring your own validator (no dep on go-playground/validator from
// fibermap):
//
//	type alwaysOK struct{}
//	func (alwaysOK) Struct(any) error { return nil }
//
//	req, _ := bind.Body[CreateTaskReq](c.Ctx, alwaysOK{})
package bind

import (
	"errors"
	"fmt"
)

// Validator is the minimal contract a request-body validator must
// satisfy. *validator.Validate from go-playground/validator/v10
// satisfies it directly.
type Validator interface {
	Struct(s any) error
}

// BodyParser is the minimal contract a Fiber context must satisfy.
// Both *fiber.Ctx and (by embedding) *fibermap.Context[T] satisfy it.
type BodyParser interface {
	BodyParser(out any) error
}

// ErrParseBody wraps a body-parsing failure.
var ErrParseBody = errors.New("bind: parse body")

// ErrValidateBody wraps a validation failure.
var ErrValidateBody = errors.New("bind: validate body")

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
