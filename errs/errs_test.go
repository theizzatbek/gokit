package errs_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/theizzatbek/fibermap/errs"
)

func TestKindString(t *testing.T) {
	cases := []struct {
		k    errs.Kind
		want string
	}{
		{errs.KindUnknown, "unknown"},
		{errs.KindNotFound, "not_found"},
		{errs.KindAlreadyExists, "already_exists"},
		{errs.KindConflict, "conflict"},
		{errs.KindValidation, "validation"},
		{errs.KindUnauthorized, "unauthorized"},
		{errs.KindPermission, "permission"},
		{errs.KindRateLimited, "rate_limited"},
		{errs.KindUnavailable, "unavailable"},
		{errs.KindTimeout, "timeout"},
		{errs.KindInternal, "internal"},
	}
	for _, tc := range cases {
		if got := tc.k.String(); got != tc.want {
			t.Errorf("Kind(%d).String() = %q, want %q", tc.k, got, tc.want)
		}
	}
}

func TestKindStringOutOfRange(t *testing.T) {
	if got := errs.Kind(99).String(); got != "unknown" {
		t.Errorf("Kind(99).String() = %q, want %q", got, "unknown")
	}
}

func TestFieldErrorJSON(t *testing.T) {
	cases := []struct {
		name string
		fe   errs.FieldError
		want string
	}{
		{
			name: "all fields",
			fe:   errs.FieldError{Field: "email", Rule: "email", Param: "", Message: "must be a valid email"},
			want: `{"field":"email","rule":"email","message":"must be a valid email"}`,
		},
		{
			name: "with param",
			fe:   errs.FieldError{Field: "password", Rule: "min", Param: "8", Message: "min 8 chars"},
			want: `{"field":"password","rule":"min","param":"8","message":"min 8 chars"}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := json.Marshal(tc.fe)
			if err != nil {
				t.Fatal(err)
			}
			if string(b) != tc.want {
				t.Errorf("got %s, want %s", b, tc.want)
			}
		})
	}
}

func TestErrorErrorMethod(t *testing.T) {
	e := &errs.Error{Kind: errs.KindNotFound, Code: "user_not_found", Message: "user 42 not found"}
	want := "not_found: user_not_found: user 42 not found"
	if got := e.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestErrorErrorMethodWithCause(t *testing.T) {
	cause := errors.New("sql: no rows")
	e := &errs.Error{Kind: errs.KindNotFound, Code: "user_not_found", Message: "user 42 not found", Cause: cause}
	want := "not_found: user_not_found: user 42 not found: sql: no rows"
	if got := e.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestErrorUnwrap(t *testing.T) {
	cause := errors.New("inner")
	e := &errs.Error{Cause: cause}
	if got := errors.Unwrap(e); got != cause {
		t.Errorf("Unwrap = %v, want %v", got, cause)
	}
}

func TestConstructors(t *testing.T) {
	cases := []struct {
		name string
		got  *errs.Error
		kind errs.Kind
	}{
		{"NotFound", errs.NotFound("user_not_found", "user 42 not found"), errs.KindNotFound},
		{"AlreadyExists", errs.AlreadyExists("user_exists", "duplicate"), errs.KindAlreadyExists},
		{"Conflict", errs.Conflict("stale", "version mismatch"), errs.KindConflict},
		{"Validation", errs.Validation("invalid", "bad input"), errs.KindValidation},
		{"Unauthorized", errs.Unauthorized("token_missing", "no token"), errs.KindUnauthorized},
		{"Permission", errs.Permission("forbidden", "admin only"), errs.KindPermission},
		{"RateLimited", errs.RateLimited("too_many", "slow down"), errs.KindRateLimited},
		{"Unavailable", errs.Unavailable("db_down", "db unreachable"), errs.KindUnavailable},
		{"Timeout", errs.Timeout("upstream_slow", "deadline exceeded"), errs.KindTimeout},
		{"Internal", errs.Internal("panic", "boom"), errs.KindInternal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got == nil {
				t.Fatal("constructor returned nil")
			}
			if tc.got.Kind != tc.kind {
				t.Errorf("Kind = %v, want %v", tc.got.Kind, tc.kind)
			}
			if tc.got.Code == "" || tc.got.Message == "" {
				t.Error("Code or Message empty")
			}
		})
	}
}

func TestValidationWithInlineDetails(t *testing.T) {
	d := errs.FieldError{Field: "x", Rule: "required", Message: "required"}
	e := errs.Validation("invalid", "bad", d)
	if len(e.Details) != 1 || e.Details[0] != d {
		t.Errorf("Details = %v, want [%v]", e.Details, d)
	}
}

func TestFConstructors(t *testing.T) {
	cases := []struct {
		name string
		got  *errs.Error
		kind errs.Kind
	}{
		{"NotFoundf", errs.NotFoundf("user_not_found", "user %d not found", 42), errs.KindNotFound},
		{"AlreadyExistsf", errs.AlreadyExistsf("dup", "duplicate %s", "alice"), errs.KindAlreadyExists},
		{"Conflictf", errs.Conflictf("stale", "version %d mismatch", 3), errs.KindConflict},
		{"Validationf", errs.Validationf("invalid", "bad %s", "input"), errs.KindValidation},
		{"Unauthorizedf", errs.Unauthorizedf("token", "missing %s", "bearer"), errs.KindUnauthorized},
		{"Permissionf", errs.Permissionf("forbidden", "role %s not allowed", "user"), errs.KindPermission},
		{"RateLimitedf", errs.RateLimitedf("too_many", "max %d req/s", 100), errs.KindRateLimited},
		{"Unavailablef", errs.Unavailablef("db", "%s unreachable", "postgres"), errs.KindUnavailable},
		{"Timeoutf", errs.Timeoutf("upstream", "deadline %s", "10s"), errs.KindTimeout},
		{"Internalf", errs.Internalf("panic", "boom %s", "now"), errs.KindInternal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got.Kind != tc.kind {
				t.Errorf("Kind = %v, want %v", tc.got.Kind, tc.kind)
			}
			if tc.got.Message == "" || tc.got.Code == "" {
				t.Error("empty Code or Message")
			}
		})
	}
}

func TestNotFoundfFormat(t *testing.T) {
	e := errs.NotFoundf("user_not_found", "user %d not found", 42)
	want := "user 42 not found"
	if e.Message != want {
		t.Errorf("Message = %q, want %q", e.Message, want)
	}
}
