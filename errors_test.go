package fibermap

import (
	"testing"
)

func TestError_Error_FormatsAllFields(t *testing.T) {
	e := &Error{
		Stage:   "mount",
		Code:    CodeUnknownHandler,
		Message: "handler not registered",
		File:    "routes.yaml",
		Line:    42,
		Path:    "groups[0].routes[1].handler",
	}

	want := "fibermap: [mount/unknown_handler] handler not registered (at groups[0].routes[1].handler) in file routes.yaml line 42"
	if s := e.Error(); s != want {
		t.Errorf("Error() = %q, want %q", s, want)
	}
}

func TestError_Error_OmitsEmptyFields(t *testing.T) {
	e := &Error{
		Stage:   "parse",
		Code:    CodeInvalidYAML,
		Message: "bad yaml",
	}

	want := "fibermap: [parse/invalid_yaml] bad yaml"
	if s := e.Error(); s != want {
		t.Errorf("Error() = %q, want %q", s, want)
	}
}
