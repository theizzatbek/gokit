package fibermap

import (
	"strings"
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

	s := e.Error()

	for _, want := range []string{"mount", "unknown_handler", "handler not registered", "routes.yaml", "42", "groups[0].routes[1].handler"} {
		if !strings.Contains(s, want) {
			t.Errorf("Error() = %q, missing %q", s, want)
		}
	}
}

func TestError_Error_OmitsEmptyFields(t *testing.T) {
	e := &Error{
		Stage:   "parse",
		Code:    CodeInvalidYAML,
		Message: "bad yaml",
	}

	s := e.Error()

	if strings.Contains(s, "line") {
		t.Errorf("Error() = %q, should not include line when 0", s)
	}
	if strings.Contains(s, "file") {
		t.Errorf("Error() = %q, should not include file when empty", s)
	}
}