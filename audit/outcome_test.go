package audit_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/theizzatbek/gokit/audit"
	xerrs "github.com/theizzatbek/gokit/errs"
)

func TestOutcomeFromError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want audit.Outcome
	}{
		{"nil is success", nil, audit.Success},
		{"unauthorized is denied", xerrs.Unauthorized("missing_token", "no"), audit.Denied},
		{"permission is denied", xerrs.Permission("missing_role", "no"), audit.Denied},
		{"wrapped permission is denied", fmt.Errorf("ctx: %w", xerrs.Permission("missing_role", "no")), audit.Denied},
		{"validation is failure", xerrs.Validation("bad_body", "no"), audit.Failure},
		{"opaque error is failure", errors.New("boom"), audit.Failure},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := audit.OutcomeFromError(tc.err); got != tc.want {
				t.Errorf("OutcomeFromError = %q, want %q", got, tc.want)
			}
		})
	}
}
