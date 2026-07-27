package svckit

import (
	"strings"
	"testing"

	"github.com/theizzatbek/gokit/auth"
	"github.com/theizzatbek/gokit/db"
)

// expectMustPanic verifies that mustFn panics with a message that
// contains the named subsystem (so the operator sees "MustDB", not a
// generic nil-pointer crash).
func expectMustPanic(t *testing.T, name string, mustFn func()) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("%s: expected panic, got none", name)
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("%s: panic value is %T, want string", name, r)
		}
		if !strings.Contains(msg, name) {
			t.Errorf("%s: panic message missing %q: %q", name, name, msg)
		}
	}()
	mustFn()
}

// TestAccessors_NilSubsystem_OptionalReturnsFalse covers the
// (nil, false) contract for every core accessor on a zero-value
// Service.
func TestAccessors_NilSubsystem_OptionalReturnsFalse(t *testing.T) {
	s := &Service[struct{}, struct{}]{}

	if got, ok := s.OptionalDB(); got != nil || ok {
		t.Errorf("OptionalDB = (%v, %v), want (nil, false)", got, ok)
	}
	if got, ok := s.OptionalAuth(); got != nil || ok {
		t.Errorf("OptionalAuth = (%v, %v), want (nil, false)", got, ok)
	}
	if got, ok := s.OptionalHasher(); got != nil || ok {
		t.Errorf("OptionalHasher = (%v, %v), want (nil, false)", got, ok)
	}
}

// TestAccessors_NilSubsystem_MustPanics covers Must* on the same
// zero-value Service — each must panic and the message must name the
// missing subsystem.
func TestAccessors_NilSubsystem_MustPanics(t *testing.T) {
	s := &Service[struct{}, struct{}]{}

	expectMustPanic(t, "MustDB", func() { _ = s.MustDB() })
	expectMustPanic(t, "MustAuth", func() { _ = s.MustAuth() })
	expectMustPanic(t, "MustHasher", func() { _ = s.MustHasher() })
}

// TestAccessors_PopulatedSubsystem_ReturnsValueAndOK covers the happy
// path: each Optional* returns (value, true) and each Must* returns
// the value without panicking when the underlying field is set.
func TestAccessors_PopulatedSubsystem_ReturnsValueAndOK(t *testing.T) {
	s := &Service[struct{}, struct{}]{
		DB:     &db.DB{},
		Auth:   &auth.Auth[struct{}]{},
		Hasher: &auth.Hasher{},
	}

	if _, ok := s.OptionalDB(); !ok {
		t.Error("OptionalDB ok = false, want true")
	}
	if _, ok := s.OptionalAuth(); !ok {
		t.Error("OptionalAuth ok = false, want true")
	}
	if _, ok := s.OptionalHasher(); !ok {
		t.Error("OptionalHasher ok = false, want true")
	}

	mustFns := []struct {
		name string
		fn   func()
	}{
		{"MustDB", func() { _ = s.MustDB() }},
		{"MustAuth", func() { _ = s.MustAuth() }},
		{"MustHasher", func() { _ = s.MustHasher() }},
	}
	for _, m := range mustFns {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s panicked when populated: %v", m.name, r)
				}
			}()
			m.fn()
		}()
	}
}
