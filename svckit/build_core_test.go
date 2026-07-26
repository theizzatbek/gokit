package svckit

import (
	"errors"
	"testing"

	"github.com/go-playground/validator/v10"
	xerrs "github.com/theizzatbek/gokit/errs"
)

func TestBuildEngine_UsesDefaultValidator(t *testing.T) {
	s := &Service[struct{}, struct{}]{opts: &options{}}

	if err := s.buildEngine(); err != nil {
		t.Fatalf("buildEngine: %v", err)
	}
	if s.Engine == nil {
		t.Fatal("Engine not built")
	}
}

func TestBuildEngine_RegistersExtraValidators(t *testing.T) {
	s := &Service[struct{}, struct{}]{opts: &options{
		extraValidators: map[string]validator.Func{
			"notblank": func(fl validator.FieldLevel) bool { return fl.Field().String() != "" },
		},
	}}

	if err := s.buildEngine(); err != nil {
		t.Fatalf("buildEngine: %v", err)
	}
}

func TestBuildEngine_BadValidatorTagIsTypedError(t *testing.T) {
	s := &Service[struct{}, struct{}]{opts: &options{
		extraValidators: map[string]validator.Func{
			"": func(fl validator.FieldLevel) bool { return true }, // empty tag is rejected
		},
	}}

	err := s.buildEngine()

	var e *xerrs.Error
	if !errors.As(err, &e) || e.Code != CodeExtraValidatorRegister {
		t.Fatalf("want Code=%q, got %#v", CodeExtraValidatorRegister, err)
	}
}

func TestSubjectKeyFn_NilAuthGivesNil(t *testing.T) {
	if fn := subjectKeyFn[struct{}](nil); fn != nil {
		t.Error("without Auth the key function must be nil")
	}
}
