package fibermap

import (
	"fmt"
	"strings"
)

// Error is the typed error returned by all fibermap operations.
// Stage is either "parse" or "mount". Code is one of the Code* constants.
type Error struct {
	Stage   string
	Code    string
	Message string
	File    string
	Line    int
	Path    string
}

func (e *Error) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "fibermap: [%s/%s] %s", e.Stage, e.Code, e.Message)
	if e.Path != "" {
		fmt.Fprintf(&b, " (at %s)", e.Path)
	}
	if e.File != "" {
		fmt.Fprintf(&b, " in file %s", e.File)
	}
	if e.Line > 0 {
		fmt.Fprintf(&b, " line %d", e.Line)
	}
	return b.String()
}

// Error codes.
const (
	// parse stage
	CodeInvalidYAML       = "invalid_yaml"
	CodeMissingField      = "missing_field"
	CodeInvalidHTTPMethod = "invalid_http_method"
	CodeMiddlewareCycle   = "middleware_cycle"

	// mount stage
	CodeUnknownHandler        = "unknown_handler"
	CodeUnknownMiddleware     = "unknown_middleware"
	CodeUnknownMiddlewareSet  = "unknown_middleware_set"
	CodeDuplicateRoute        = "duplicate_route"
	CodeMissingContextBuilder = "missing_context_builder"
	CodeMissingRoleChecker    = "missing_role_checker"
	CodeAlreadyMounted        = "already_mounted"

	// register stage
	CodeDuplicateRegistration = "duplicate_registration"
)