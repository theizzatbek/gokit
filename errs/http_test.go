package errs_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/theizzatbek/gokit/errs"
)

func TestHTTPMapping(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"NotFound", errs.NotFound("user_not_found", "x"), 404, "user_not_found"},
		{"AlreadyExists", errs.AlreadyExists("dup", "x"), 409, "dup"},
		{"Conflict", errs.Conflict("stale", "x"), 409, "stale"},
		{"Validation", errs.Validation("invalid", "x"), 400, "invalid"},
		{"Unauthorized", errs.Unauthorized("token", "x"), 401, "token"},
		{"Permission", errs.Permission("forbidden", "x"), 403, "forbidden"},
		{"RateLimited", errs.RateLimited("too_many", "x"), 429, "too_many"},
		{"Unavailable", errs.Unavailable("db", "x"), 503, "db"},
		{"Timeout", errs.Timeout("upstream", "x"), 504, "upstream"},
		{"Internal", errs.Internal("boom", "x"), 500, "boom"},
		{"unknown error", errors.New("raw"), 500, "internal_error"},
		{"wrapped *Error", errs.Wrap(errs.NotFound("u", "x"), errs.KindInternal, "outer", "o"), 500, "outer"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, body := errs.HTTP(tc.err)
			if status != tc.status {
				t.Errorf("status = %d, want %d", status, tc.status)
			}
			if body.Code != tc.code {
				t.Errorf("body.Code = %q, want %q", body.Code, tc.code)
			}
		})
	}
}

func TestHTTPNil(t *testing.T) {
	status, body := errs.HTTP(nil)
	if status != 200 {
		t.Errorf("status = %d, want 200", status)
	}
	if body.Code != "" || body.Message != "" {
		t.Errorf("body = %+v, want empty", body)
	}
}

func TestHTTPBodyJSONOmitsEmptyDetails(t *testing.T) {
	_, body := errs.HTTP(errs.NotFound("u", "x"))
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	want := `{"code":"u","message":"x"}`
	if got != want {
		t.Errorf("JSON = %s, want %s", got, want)
	}
}

func TestHTTPBodyJSONIncludesDetails(t *testing.T) {
	d := errs.FieldError{Field: "email", Rule: "required", Message: "required"}
	_, body := errs.HTTP(errs.Validation("invalid", "x", d))
	b, _ := json.Marshal(body)
	got := string(b)
	want := `{"code":"invalid","message":"x","details":[{"field":"email","rule":"required","message":"required"}]}`
	if got != want {
		t.Errorf("JSON = %s, want %s", got, want)
	}
}
