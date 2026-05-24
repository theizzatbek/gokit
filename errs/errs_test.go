package errs_test

import (
	"encoding/json"
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
