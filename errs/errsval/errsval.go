// Package errsval converts go-playground/validator errors into *errs.Error.
//
// Kept in a subpackage so the core errs/ package stays stdlib-only.
package errsval

import (
	"errors"
	"fmt"

	"github.com/go-playground/validator/v10"
	"github.com/theizzatbek/fibermap/errs"
)

// FromValidator converts a validator.ValidationErrors into a *errs.Error of
// kind Validation, with Details populated from each field error. Code is
// "validation_failed", Message is "request validation failed".
//
// If err is nil, returns nil. If err is not validator.ValidationErrors,
// returns it unchanged so callers can chain it through.
func FromValidator(err error) error {
	if err == nil {
		return nil
	}
	var vErrs validator.ValidationErrors
	ok := errors.As(err, &vErrs)
	if !ok {
		return err
	}
	details := make([]errs.FieldError, 0, len(vErrs))
	for _, fe := range vErrs {
		details = append(details, errs.FieldError{
			Field:   fe.Namespace(),
			Rule:    fe.Tag(),
			Param:   fe.Param(),
			Message: humanMessage(fe),
		})
	}
	return errs.Validation("validation_failed", "request validation failed", details...)
}

func humanMessage(fe validator.FieldError) string {
	switch fe.Tag() {
	case "required":
		return fmt.Sprintf("%s is required", fe.Field())
	case "email":
		return fmt.Sprintf("%s must be a valid email", fe.Field())
	case "min":
		return fmt.Sprintf("%s must be at least %s", fe.Field(), fe.Param())
	case "max":
		return fmt.Sprintf("%s must be at most %s", fe.Field(), fe.Param())
	case "len":
		return fmt.Sprintf("%s must be exactly %s in length", fe.Field(), fe.Param())
	case "oneof":
		return fmt.Sprintf("%s must be one of: %s", fe.Field(), fe.Param())
	default:
		return fmt.Sprintf("%s failed %s validation", fe.Field(), fe.Tag())
	}
}
