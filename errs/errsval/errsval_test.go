package errsval_test

import (
	"errors"
	"testing"

	"github.com/go-playground/validator/v10"
	"github.com/theizzatbek/fibermap/errs"
	"github.com/theizzatbek/fibermap/errs/errsval"
)

type signupReq struct {
	Email    string `validate:"required,email"`
	Password string `validate:"required,min=8"`
}

func TestFromValidatorConvertsValidationErrors(t *testing.T) {
	v := validator.New()
	err := v.Struct(signupReq{Email: "not-an-email", Password: "short"})
	if err == nil {
		t.Fatal("expected validation errors")
	}
	got := errsval.FromValidator(err)
	var e *errs.Error
	if !errors.As(got, &e) {
		t.Fatalf("FromValidator returned %T, want *errs.Error", got)
	}
	if e.Kind != errs.KindValidation {
		t.Errorf("Kind = %v, want Validation", e.Kind)
	}
	if e.Code != "validation_failed" {
		t.Errorf("Code = %q, want validation_failed", e.Code)
	}
	if len(e.Details) != 2 {
		t.Fatalf("Details = %v, want 2 entries", e.Details)
	}
	gotEmail := findDetail(e.Details, "signupReq.Email")
	if gotEmail == nil || gotEmail.Rule != "email" {
		t.Errorf("email FieldError = %+v, want Rule=email", gotEmail)
	}
	gotPw := findDetail(e.Details, "signupReq.Password")
	if gotPw == nil || gotPw.Rule != "min" || gotPw.Param != "8" {
		t.Errorf("password FieldError = %+v, want Rule=min Param=8", gotPw)
	}
}

func TestFromValidatorPassesThroughNonValidatorError(t *testing.T) {
	raw := errors.New("not a validator error")
	got := errsval.FromValidator(raw)
	if got != raw {
		t.Errorf("got %v, want %v (pass-through)", got, raw)
	}
}

func TestFromValidatorNil(t *testing.T) {
	if errsval.FromValidator(nil) != nil {
		t.Error("FromValidator(nil) should be nil")
	}
}

func findDetail(ds []errs.FieldError, field string) *errs.FieldError {
	for i := range ds {
		if ds[i].Field == field {
			return &ds[i]
		}
	}
	return nil
}

// TestFromValidatorHumanMessages exercises every branch of humanMessage so the
// per-tag wording stays covered as new rules are added.
type humanMsgReq struct {
	Name     string `validate:"required"`
	Username string `validate:"max=5"`
	Code     string `validate:"len=4"`
	Role     string `validate:"oneof=admin user"`
	Age      int    `validate:"gte=18"`
}

func TestFromValidatorHumanMessages(t *testing.T) {
	v := validator.New()
	// Provide values that violate every rule above. Name empty triggers required;
	// Username too long triggers max; Code wrong length triggers len; Role not in
	// list triggers oneof; Age below 18 triggers gte (the default branch).
	err := v.Struct(humanMsgReq{
		Name:     "",
		Username: "too-long-username",
		Code:     "xx",
		Role:     "guest",
		Age:      10,
	})
	if err == nil {
		t.Fatal("expected validation errors")
	}
	got := errsval.FromValidator(err)
	var e *errs.Error
	if !errors.As(got, &e) {
		t.Fatalf("FromValidator returned %T, want *errs.Error", got)
	}
	want := map[string]string{
		"humanMsgReq.Name":     "Name is required",
		"humanMsgReq.Username": "Username must be at most 5",
		"humanMsgReq.Code":     "Code must be exactly 4 in length",
		"humanMsgReq.Role":     "Role must be one of: admin user",
		"humanMsgReq.Age":      "Age failed gte validation",
	}
	for field, wantMsg := range want {
		d := findDetail(e.Details, field)
		if d == nil {
			t.Errorf("missing detail for %s", field)
			continue
		}
		if d.Message != wantMsg {
			t.Errorf("%s message = %q, want %q", field, d.Message, wantMsg)
		}
	}
}
