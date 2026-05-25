package apimap

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	xerrs "github.com/theizzatbek/gokit/errs"
)

func TestSubstitutePath_Happy(t *testing.T) {
	got, err := substitutePath("/users/{username}/repos", []string{"username"},
		map[string]string{"username": "torvalds"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "/users/torvalds/repos" {
		t.Errorf("got %q, want /users/torvalds/repos", got)
	}
}

func TestSubstitutePath_URLEscapes(t *testing.T) {
	got, err := substitutePath("/users/{name}", []string{"name"},
		map[string]string{"name": "foo/bar baz"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "/users/foo%2Fbar%20baz" {
		t.Errorf("got %q, want /users/foo%%2Fbar%%20baz", got)
	}
}

func TestSubstitutePath_MissingVar(t *testing.T) {
	_, err := substitutePath("/users/{username}", []string{"username"},
		map[string]string{})
	if err == nil {
		t.Fatal("nil error, want CodeMissingPathVar")
	}
	var e *xerrs.Error
	if !errors.As(err, &e) || e.Code != CodeMissingPathVar {
		t.Errorf("err = %v, want code %s", err, CodeMissingPathVar)
	}
}

func TestSubstitutePath_UnknownVar(t *testing.T) {
	_, err := substitutePath("/users/{username}", []string{"username"},
		map[string]string{"username": "x", "extra": "y"})
	if err == nil {
		t.Fatal("nil error, want CodeUnknownPathVar")
	}
	var e *xerrs.Error
	if !errors.As(err, &e) || e.Code != CodeUnknownPathVar {
		t.Errorf("err = %v, want code %s", err, CodeUnknownPathVar)
	}
}

func TestEncodeBody_None(t *testing.T) {
	r, ct, err := encodeBody("none", map[string]string{"x": "y"})
	if err != nil {
		t.Fatal(err)
	}
	if r != nil {
		t.Errorf("reader = %v, want nil", r)
	}
	if ct != "" {
		t.Errorf("content-type = %q, want empty", ct)
	}
}

func TestEncodeBody_JSON(t *testing.T) {
	r, ct, err := encodeBody("json", map[string]int{"n": 42})
	if err != nil {
		t.Fatal(err)
	}
	if ct != "application/json" {
		t.Errorf("content-type = %q", ct)
	}
	b, _ := io.ReadAll(r)
	if string(b) != `{"n":42}` {
		t.Errorf("body = %q", b)
	}
}

func TestEncodeBody_FormFromValues(t *testing.T) {
	v := url.Values{"a": []string{"1"}, "b": []string{"x"}}
	r, ct, err := encodeBody("form", v)
	if err != nil {
		t.Fatal(err)
	}
	if ct != "application/x-www-form-urlencoded" {
		t.Errorf("content-type = %q", ct)
	}
	b, _ := io.ReadAll(r)
	got := string(b)
	if got != "a=1&b=x" {
		t.Errorf("body = %q, want a=1&b=x", got)
	}
}

func TestEncodeBody_FormFromMap(t *testing.T) {
	r, ct, err := encodeBody("form", map[string]string{"k": "v"})
	if err != nil {
		t.Fatal(err)
	}
	if ct != "application/x-www-form-urlencoded" {
		t.Errorf("content-type = %q", ct)
	}
	b, _ := io.ReadAll(r)
	if string(b) != "k=v" {
		t.Errorf("body = %q", b)
	}
}

func TestEncodeBody_FormBadType(t *testing.T) {
	type oddBody struct{ X int }
	_, _, err := encodeBody("form", oddBody{X: 1})
	if err == nil {
		t.Fatal("nil error, want CodeUnsupportedBodyType")
	}
	var e *xerrs.Error
	if !errors.As(err, &e) || e.Code != CodeUnsupportedBodyType {
		t.Errorf("err = %v, want code %s", err, CodeUnsupportedBodyType)
	}
}

func TestEncodeBody_Raw(t *testing.T) {
	r, ct, err := encodeBody("raw", strings.NewReader("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if ct != "" {
		t.Errorf("content-type = %q, want empty (caller-supplied)", ct)
	}
	b, _ := io.ReadAll(r)
	if string(b) != "hello" {
		t.Errorf("body = %q", b)
	}
}

func TestEncodeBody_RawBadType(t *testing.T) {
	_, _, err := encodeBody("raw", 42)
	if err == nil {
		t.Fatal("nil error, want CodeUnsupportedBodyType")
	}
	var e *xerrs.Error
	if !errors.As(err, &e) || e.Code != CodeUnsupportedBodyType {
		t.Errorf("err = %v, want code %s", err, CodeUnsupportedBodyType)
	}
}

func TestMergeHeaders_Precedence(t *testing.T) {
	defaults := map[string]string{"X-A": "from-default", "X-B": "from-default"}
	endpoint := map[string]string{"X-B": "from-endpoint", "X-C": "from-endpoint"}
	call := http.Header{"X-C": []string{"from-call"}, "X-D": []string{"from-call"}}
	got := mergeHeaders(defaults, endpoint, call)
	tests := []struct{ key, want string }{
		{"X-A", "from-default"},
		{"X-B", "from-endpoint"},
		{"X-C", "from-call"},
		{"X-D", "from-call"},
	}
	for _, tt := range tests {
		if got.Get(tt.key) != tt.want {
			t.Errorf("header %s = %q, want %q", tt.key, got.Get(tt.key), tt.want)
		}
	}
}
