package errs_test

import (
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
