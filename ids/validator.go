package ids

import (
	"fmt"

	"github.com/go-playground/validator/v10"
)

// ValidatorTag is the struct-tag name [RegisterValidator] wires.
// Stable across the kit's semver contract; documented here so
// callers can grep for it.
const ValidatorTag = "id_prefix"

// RegisterValidator wires the `validate:"id_prefix=<prefix>"` tag
// on the supplied *validator.Validate so DTOs can validate inbound
// IDs declaratively:
//
//	type CreatePolicyReq struct {
//	    ProductID string `json:"product_id" validate:"required,id_prefix=prod_"`
//	}
//
// The tag enforces both halves of [Parse]: the field's string
// starts with the param-supplied prefix AND the tail is a valid
// 26-char Crockford-Base32 ULID. A field that fails either half
// surfaces a normal validator.ValidationErrors entry — the
// fibermap.ErrsvalBindError handler picks it up as a 400 with
// per-field Details[] without any additional wiring.
//
// Returns the error from validator.RegisterValidation (rare: only
// on a duplicate registration on the same *validator.Validate
// instance, which is itself a programmer-error condition).
//
// Pure-stdlib safety: the registered function does NOT depend on
// any package state beyond the `prefix` param, so it is safe to
// call against shared *validator.Validate instances (the typical
// case: [service.New] builds one default validator and the same
// instance binds every handler's bind step).
func RegisterValidator(v *validator.Validate) error {
	return v.RegisterValidation(ValidatorTag, idPrefixValidator)
}

// idPrefixValidator implements the validator.Func contract for the
// id_prefix tag. The validator package exposes the param (the
// expected prefix) via fl.Param().
func idPrefixValidator(fl validator.FieldLevel) bool {
	prefix := fl.Param()
	if prefix == "" {
		// A `validate:"id_prefix"` without a param is operator error.
		// Surface as a validation failure rather than panic so the
		// fix bubbles up through normal 400 responses.
		return false
	}
	s := fl.Field().String()
	_, err := Parse(prefix, s)
	return err == nil
}

// Tag is a convenience for callers who want to assemble the tag
// string programmatically (typically in code-generated DTOs):
//
//	`validate:"required,` + ids.Tag("prod_") + `"`
func Tag(prefix string) string {
	return fmt.Sprintf("%s=%s", ValidatorTag, prefix)
}
