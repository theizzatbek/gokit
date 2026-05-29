package errs_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/theizzatbek/gokit/errs"
)

func TestAll_NilReturnsNil(t *testing.T) {
	if got := errs.All(nil); got != nil {
		t.Errorf("All(nil) = %v, want nil", got)
	}
}

func TestAll_SingleErrorReturnsIt(t *testing.T) {
	e := errs.Validation("bad", "boom")
	got := errs.All(e)
	if len(got) != 1 || got[0] != e {
		t.Errorf("All(single) = %v, want [%v]", got, e)
	}
}

func TestAll_JoinFlattensChildren(t *testing.T) {
	a := errs.Validation("c1", "m1")
	b := errs.Conflict("c2", "m2")
	c := errs.NotFound("c3", "m3")
	joined := errors.Join(a, b, c)

	got := errs.All(joined)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3 (got %v)", len(got), got)
	}
	codes := []string{got[0].Code, got[1].Code, got[2].Code}
	want := []string{"c1", "c2", "c3"}
	for i := range want {
		if codes[i] != want[i] {
			t.Errorf("codes[%d] = %q, want %q", i, codes[i], want[i])
		}
	}
}

func TestAll_NestedJoinFlattens(t *testing.T) {
	inner := errors.Join(errs.Validation("a", "1"), errs.Validation("b", "2"))
	outer := errors.Join(inner, errs.Validation("c", "3"))

	got := errs.All(outer)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3 (got %v)", len(got), got)
	}
	for i, want := range []string{"a", "b", "c"} {
		if got[i].Code != want {
			t.Errorf("got[%d].Code = %q, want %q", i, got[i].Code, want)
		}
	}
}

func TestAll_NonErrorMembersAreSkipped(t *testing.T) {
	plain := fmt.Errorf("not an *errs.Error")
	typed := errs.NotFound("x", "y")
	joined := errors.Join(plain, typed)

	got := errs.All(joined)
	if len(got) != 1 || got[0] != typed {
		t.Errorf("All = %v, want [%v]", got, typed)
	}
}

func TestAll_WrappedCauseChainSurfaces(t *testing.T) {
	root := errs.NotFound("root", "missing")
	wrapped := errs.Wrap(root, errs.KindInternal, "wrapped", "while doing X")

	got := errs.All(wrapped)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (outer wrapper + root cause)", len(got))
	}
	if got[0].Code != "wrapped" || got[1].Code != "root" {
		t.Errorf("got codes = [%s, %s], want [wrapped, root]", got[0].Code, got[1].Code)
	}
}

func TestAll_EmptyJoinReturnsEmpty(t *testing.T) {
	// errors.Join(nil, nil, ...) returns nil — All should mirror.
	if got := errs.All(errors.Join(nil, nil)); got != nil {
		t.Errorf("All(empty join) = %v, want nil", got)
	}
}
