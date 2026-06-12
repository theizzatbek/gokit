package service

import (
	"context"
	"errors"
	"testing"

	"github.com/go-playground/validator/v10"
)

// Tests for v1.1.0 P2-12: service.WithExtraValidators(map[string]validator.Func).
//
// Contract:
//   - Multiple WithExtraValidators calls accumulate; later calls
//     overwrite same-tag registrations (last-write-wins).
//   - Empty / nil maps are no-ops.
//   - When WithValidator was NOT passed, the kit registers extras
//     on the kit-default *validator.Validate and that instance becomes
//     the Engine.SetValidator value.
//   - When WithValidator WAS passed, extras are silently ignored —
//     the kit refuses to mutate a caller-supplied instance.
//   - A validator.RegisterValidation error at boot surfaces as
//     *errs.Error{Code: CodeExtraValidatorRegister} from service.New.

// isSlugChars accepts identifiers made of lowercase letters,
// digits, and `_`/`-`. Used in multiple tests below.
func isSlugChars(fl validator.FieldLevel) bool {
	s := fl.Field().String()
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '-':
		default:
			return false
		}
	}
	return true
}

func isLengthCustom(fl validator.FieldLevel) bool {
	return len(fl.Field().String()) >= 3
}

func TestWithExtraValidators_RegistersOnDefaultValidator(t *testing.T) {
	svc := newServiceForSecHeadersTest(t, WithExtraValidators(map[string]validator.Func{
		"slug_chars": isSlugChars,
	}))

	v, ok := svc.Engine.Validator().(*validator.Validate)
	if !ok {
		t.Fatalf("validator is not *validator.Validate, got %T", svc.Engine.Validator())
	}

	type DTO struct {
		Slug string `validate:"required,slug_chars"`
	}
	if err := v.Struct(DTO{Slug: "good-one_42"}); err != nil {
		t.Errorf("valid slug failed validation: %v", err)
	}
	if err := v.Struct(DTO{Slug: "Bad!Slug"}); err == nil {
		t.Error("invalid slug passed validation; expected failure")
	}
}

func TestWithExtraValidators_MultipleAccumulate(t *testing.T) {
	svc := newServiceForSecHeadersTest(t,
		WithExtraValidators(map[string]validator.Func{"slug_chars": isSlugChars}),
		WithExtraValidators(map[string]validator.Func{"len_3plus": isLengthCustom}),
	)
	v := svc.Engine.Validator().(*validator.Validate)

	type DTO struct {
		A string `validate:"slug_chars"`
		B string `validate:"len_3plus"`
	}
	if err := v.Struct(DTO{A: "ok-id", B: "abc"}); err != nil {
		t.Errorf("both tags should pass: %v", err)
	}
}

func TestWithExtraValidators_LaterOverwritesEarlier(t *testing.T) {
	// First registration says "accept anything". Second registration
	// is the strict slug check — must win on the same `chk` tag.
	acceptAll := func(validator.FieldLevel) bool { return true }
	svc := newServiceForSecHeadersTest(t,
		WithExtraValidators(map[string]validator.Func{"chk": acceptAll}),
		WithExtraValidators(map[string]validator.Func{"chk": isSlugChars}),
	)
	v := svc.Engine.Validator().(*validator.Validate)

	type DTO struct {
		V string `validate:"chk"`
	}
	if err := v.Struct(DTO{V: "Bad!Slug"}); err == nil {
		t.Error("acceptAll won over isSlugChars; last-write-wins broken")
	}
}

func TestWithExtraValidators_EmptyMap_NoOp(t *testing.T) {
	// Calling with nil or empty map must NOT crash and must NOT
	// allocate a registrations map (regression guard).
	svc := newServiceForSecHeadersTest(t,
		WithExtraValidators(nil),
		WithExtraValidators(map[string]validator.Func{}),
	)
	if svc.opts.extraValidators != nil {
		t.Errorf("extraValidators allocated for empty calls: %v", svc.opts.extraValidators)
	}
}

func TestWithExtraValidators_DefersWhenWithValidatorPresent(t *testing.T) {
	// Caller's WithValidator instance is used verbatim. Extras MUST
	// NOT be registered on it (the kit refuses to mutate a
	// caller-shared instance). Identity check on the validator
	// pointer is sufficient — calling Struct() on an unknown tag
	// panics in validator/v10, so we can't probe by exercising the
	// non-registration directly.
	callerV := validator.New()
	svc := newServiceForSecHeadersTest(t,
		WithValidator(callerV),
		WithExtraValidators(map[string]validator.Func{
			"slug_chars": isSlugChars,
		}),
	)

	if got := svc.Engine.Validator(); got != callerV {
		t.Errorf("engine validator (%p) != caller-supplied (%p)", got, callerV)
	}
}

// Note: validator/v10 RegisterValidation PANICS on attempts to
// overwrite a built-in tag (e.g. "required") rather than returning
// an error. The kit can't gracefully wrap that into a typed *errs.Error
// without a defer-recover that would cost more than it's worth.
// Operators wiring WithExtraValidators are expected to use
// non-builtin tag names (the whole point of the API). The wrapped
// `*errs.Error{Code: CodeExtraValidatorRegister}` error path in
// buildEngine stays as defensive coding for any future
// RegisterValidation failure mode that doesn't panic.

// TestExtraValidators_NewSucceedsWithRealService is a smoke test
// that goes through service.New (vs the lighter newServiceForSecHeadersTest
// helper above) and exercises the engine validator end-to-end.
func TestExtraValidators_NewSucceedsWithRealService(t *testing.T) {
	cfg := Config{}
	cfg.Service.LogLevel = "error"
	svc, err := New[map[string]any, any](context.Background(), cfg,
		WithExtraValidators(map[string]validator.Func{
			"slug_chars": isSlugChars,
		}),
	)
	if err != nil {
		t.Fatalf("service.New: %v", err)
	}
	t.Cleanup(svc.Close)

	v, ok := svc.Engine.Validator().(*validator.Validate)
	if !ok {
		t.Fatalf("Engine.Validator() type = %T, want *validator.Validate", svc.Engine.Validator())
	}
	type DTO struct {
		Slug string `validate:"required,slug_chars"`
	}
	if err := v.Struct(DTO{Slug: "good-id"}); err != nil {
		t.Errorf("valid DTO failed: %v", err)
	}
	if err := v.Struct(DTO{Slug: "BAD!"}); err == nil {
		t.Error("invalid DTO passed validation")
	}
}

// Imports kept for the lifetime-related compilation guard above.
var _ = errors.As
var _ = context.Background
