package ids

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/go-playground/validator/v10"
	xerrs "github.com/theizzatbek/gokit/errs"
)

func TestNew_PrefixedAndCanonical(t *testing.T) {
	id := New("prod_")
	if !strings.HasPrefix(id, "prod_") {
		t.Fatalf("New('prod_') = %q, missing prefix", id)
	}
	tail := strings.TrimPrefix(id, "prod_")
	if len(tail) != suffixLen {
		t.Errorf("suffix length = %d, want %d", len(tail), suffixLen)
	}
}

func TestNew_EmptyPrefix_PureULID(t *testing.T) {
	id := New("")
	if len(id) != suffixLen {
		t.Errorf("empty-prefix New length = %d, want %d", len(id), suffixLen)
	}
}

func TestNew_RoundTripThroughParse(t *testing.T) {
	id := New("user_")
	raw, err := Parse("user_", id)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if raw == [16]byte{} {
		t.Error("raw bytes all zero — entropy didn't fire")
	}
	// Format reproduces the original string.
	if got := Format("user_", raw); got != id {
		t.Errorf("Format round-trip = %q, want %q", got, id)
	}
}

func TestNew_MonotonicWithinSameMillisecond(t *testing.T) {
	// The ULID monotonic-entropy contract guarantees that two
	// consecutive New() calls in the same ms produce strictly
	// increasing IDs by string compare. Hammer with 1k calls to be
	// sure none of them slip past the lock and produce a
	// non-monotonic result.
	prev := New("x_")
	for i := 0; i < 1000; i++ {
		cur := New("x_")
		if cur <= prev {
			t.Fatalf("monotonicity violated: cur=%q <= prev=%q (iteration %d)", cur, prev, i)
		}
		prev = cur
	}
}

func TestNew_ConcurrencySafe(t *testing.T) {
	// Two goroutines blasting New() in parallel. The package serialises
	// access to the entropy source via mu; the monotonic-entropy panic
	// (ulid.MustNew on overflow) is the failure mode we're guarding
	// against. We don't assert ordering here — just that no goroutine
	// panics.
	const workers = 8
	const each = 1000
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < each; j++ {
				_ = New("z_")
			}
		}()
	}
	wg.Wait()
}

func TestParse_HappyPath(t *testing.T) {
	id := New("acc_")
	raw, err := Parse("acc_", id)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if raw == [16]byte{} {
		t.Error("raw all-zero")
	}
}

func TestParse_BadPrefix_CodeAndKind(t *testing.T) {
	cases := map[string]string{
		"completely-wrong-prefix": "prod_01ARZ3NDEKTSV4RRFFQ69G5FAV", // valid ULID, wrong prefix
		"missing-prefix":          "01ARZ3NDEKTSV4RRFFQ69G5FAV",      // valid ULID, no prefix at all
		"empty-input":             "",
		"prefix-only":             "user_",
		"too-short":               "u",
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse("user_", input)
			if err == nil {
				t.Fatalf("Parse(%q) err = nil, want CodeBadPrefix or CodeBadSuffix", input)
			}
			var e *xerrs.Error
			if !errors.As(err, &e) {
				t.Fatalf("not a *errs.Error: %v", err)
			}
			// "prefix-only" is the boundary case — input matches the
			// prefix exactly so the prefix check passes; the tail is
			// empty and fails the length check → CodeBadSuffix.
			if name == "prefix-only" {
				if e.Code != CodeBadSuffix {
					t.Errorf("code = %q, want %q (empty tail → bad suffix)", e.Code, CodeBadSuffix)
				}
				return
			}
			if e.Code != CodeBadPrefix {
				t.Errorf("code = %q, want %q", e.Code, CodeBadPrefix)
			}
		})
	}
}

func TestParse_BadSuffix_CodeAndKind(t *testing.T) {
	cases := map[string]string{
		"too-short-suffix":     "user_01H",
		"too-long-suffix":      "user_01H0000000000000000000000000XXXX",
		"illegal-character":    "user_01H!!!!!!!!!!!!!!!!!!!!!!",
		"valid-prefix-garbage": "user_oh-no-this-is-not-a-ulid",
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse("user_", input)
			if err == nil {
				t.Fatalf("Parse(%q) err = nil, want CodeBadSuffix", input)
			}
			var e *xerrs.Error
			if !errors.As(err, &e) {
				t.Fatalf("not a *errs.Error: %v", err)
			}
			if e.Code != CodeBadSuffix {
				t.Errorf("code = %q, want %q", e.Code, CodeBadSuffix)
			}
		})
	}
}

func TestFormat_FromRaw_ProducesParseableString(t *testing.T) {
	id := New("xyz_")
	raw, _ := Parse("xyz_", id)
	roundtrip := Format("xyz_", raw)
	if roundtrip != id {
		t.Errorf("Format round-trip = %q, want %q", roundtrip, id)
	}
	// And Parse'ing the formatted string gives the original raw back.
	raw2, err := Parse("xyz_", roundtrip)
	if err != nil {
		t.Fatalf("Parse(Format(raw)): %v", err)
	}
	if raw2 != raw {
		t.Error("raw bytes changed across Format → Parse")
	}
}

func TestFormat_ChangesPrefixOnly(t *testing.T) {
	// Same raw bytes with two different prefixes must produce two
	// strings that differ only in the prefix portion.
	id := New("a_")
	raw, _ := Parse("a_", id)
	withB := Format("b_", raw)
	if strings.TrimPrefix(withB, "b_") != strings.TrimPrefix(id, "a_") {
		t.Error("re-formatting changed the suffix")
	}
}

// ---------------------------------------------------------------------
// Validator-tag tests (RegisterValidator + Tag helper).
// ---------------------------------------------------------------------

func TestRegisterValidator_HappyPath(t *testing.T) {
	v := validator.New(validator.WithRequiredStructEnabled())
	if err := RegisterValidator(v); err != nil {
		t.Fatalf("RegisterValidator: %v", err)
	}

	type DTO struct {
		ID string `validate:"required,id_prefix=prod_"`
	}

	good := DTO{ID: New("prod_")}
	if err := v.Struct(good); err != nil {
		t.Errorf("valid prod_ id failed validation: %v", err)
	}
}

func TestRegisterValidator_WrongPrefix_Fails(t *testing.T) {
	v := validator.New()
	if err := RegisterValidator(v); err != nil {
		t.Fatal(err)
	}

	type DTO struct {
		ID string `validate:"id_prefix=prod_"`
	}

	bad := DTO{ID: New("user_")} // wrong prefix
	if err := v.Struct(bad); err == nil {
		t.Error("wrong-prefix id passed validation")
	}
}

func TestRegisterValidator_GarbageSuffix_Fails(t *testing.T) {
	v := validator.New()
	if err := RegisterValidator(v); err != nil {
		t.Fatal(err)
	}

	type DTO struct {
		ID string `validate:"id_prefix=prod_"`
	}

	bad := DTO{ID: "prod_not-a-ulid"}
	if err := v.Struct(bad); err == nil {
		t.Error("garbage-suffix id passed validation")
	}
}

func TestRegisterValidator_EmptyParam_Fails(t *testing.T) {
	v := validator.New()
	if err := RegisterValidator(v); err != nil {
		t.Fatal(err)
	}

	// `id_prefix` without a param is operator-error; the validator
	// fails the field rather than panic.
	type DTO struct {
		ID string `validate:"id_prefix"`
	}

	if err := v.Struct(DTO{ID: "anything"}); err == nil {
		t.Error("missing-param tag passed validation; expected to fail")
	}
}

func TestTag_AssemblesCanonicalString(t *testing.T) {
	if got := Tag("prod_"); got != "id_prefix=prod_" {
		t.Errorf("Tag('prod_') = %q, want %q", got, "id_prefix=prod_")
	}
	if got := Tag(""); got != "id_prefix=" {
		t.Errorf("Tag('') = %q, want %q (empty param produces 'id_prefix=')", got, "id_prefix=")
	}
}
