package errs_test

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/theizzatbek/gokit/errs"
)

// Example builds a typed domain error and prints its string form.
// The format is "kind: code: message" — stable enough to log and grep.
func Example() {
	err := errs.NotFound("user_not_found", "no user with that id")
	fmt.Println(err)
	// Output: not_found: user_not_found: no user with that id
}

// ExampleWrap wraps a lower-layer cause while attaching a Kind and Code.
// The cause stays reachable through errors.Is / errors.As, so callers can
// both match the sentinel and read the typed Kind/Code.
func ExampleWrap() {
	errNoRows := errors.New("sql: no rows in result set")

	err := errs.Wrap(errNoRows, errs.KindNotFound, "user_not_found", "load user")

	fmt.Println(err)
	fmt.Println(errors.Is(err, errNoRows))
	// Output:
	// not_found: user_not_found: load user: sql: no rows in result set
	// true
}

// ExampleHTTP maps a domain error to an HTTP status code and a
// JSON-serialisable response body. KindValidation → 400, and any inline
// FieldError details ride along in the body.
func ExampleHTTP() {
	err := errs.Validation("invalid_body", "request body failed validation",
		errs.FieldError{Field: "email", Rule: "required", Message: "email is required"})

	status, body := errs.HTTP(err)
	out, _ := json.Marshal(body)

	fmt.Println(status)
	fmt.Println(string(out))
	// Output:
	// 400
	// {"code":"invalid_body","message":"request body failed validation","details":[{"field":"email","rule":"required","message":"email is required"}]}
}
