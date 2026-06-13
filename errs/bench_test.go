package errs_test

import (
	"errors"
	"testing"

	"github.com/theizzatbek/gokit/errs"
)

// Package-level sinks defeat dead-code elimination: storing each result
// to a global stops the compiler from optimising the benchmarked call
// away when its return value would otherwise be discarded.
var (
	sinkStatus int
	sinkResp   errs.Response
	sinkErr    error
)

// BenchmarkHTTP measures the error→(status, body) mapping that runs once
// per failed request in the error handler.
func BenchmarkHTTP(b *testing.B) {
	err := errs.Validation("invalid_body", "request body failed validation",
		errs.FieldError{Field: "email", Rule: "required", Message: "email is required"})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkStatus, sinkResp = errs.HTTP(err)
	}
}

// BenchmarkWrap measures constructing a wrapped typed error — the common
// path when adapting a lower-layer error at a boundary.
func BenchmarkWrap(b *testing.B) {
	cause := errors.New("boom")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkErr = errs.Wrap(cause, errs.KindInternal, "load_failed", "load user")
	}
}
