package ids_test

import (
	"errors"
	"fmt"
	"strings"

	"github.com/theizzatbek/gokit/errs"
	"github.com/theizzatbek/gokit/ids"
)

// Example mints a fresh prefixed ULID and shows the round-trip:
// New → Parse → Format returns the original string. New is time-based
// and random, so the value differs every run — the invariants below
// hold regardless.
func Example() {
	id := ids.New("user_")

	raw, err := ids.Parse("user_", id)

	fmt.Println(strings.HasPrefix(id, "user_"))
	fmt.Println(err == nil)
	fmt.Println(ids.Format("user_", raw) == id)
	// Output:
	// true
	// true
	// true
}

// ExampleParse validates a known ID and strips the prefix, returning the
// raw 16 bytes you'd store in a Postgres uuid column. Format is the
// inverse and always re-emits uppercase Crockford Base32.
func ExampleParse() {
	raw, err := ids.Parse("user_", "user_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if err != nil {
		panic(err)
	}
	fmt.Println(ids.Format("user_", raw))
	// Output: user_01ARZ3NDEKTSV4RRFFQ69G5FAV
}

// ExampleParse_badPrefix shows the error contract: a prefix mismatch is a
// typed *errs.Error of KindValidation with a stable Code, so HTTP handlers
// map it to 400 without special-casing.
func ExampleParse_badPrefix() {
	_, err := ids.Parse("user_", "acct_01ARZ3NDEKTSV4RRFFQ69G5FAV")

	var e *errs.Error
	if errors.As(err, &e) {
		fmt.Println(e.Kind, e.Code)
	}
	// Output: validation id_bad_prefix
}
