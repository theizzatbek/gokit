package reqctx

import (
	"context"
	"testing"
)

func TestWithRequestID_RoundTrip(t *testing.T) {
	ctx := WithRequestID(context.Background(), "abc-123")
	if got := RequestIDFromContext(ctx); got != "abc-123" {
		t.Fatalf("got %q want %q", got, "abc-123")
	}
}

func TestRequestIDFromContext_Empty(t *testing.T) {
	if got := RequestIDFromContext(context.Background()); got != "" {
		t.Fatalf("got %q want empty", got)
	}
}

func TestWithRequestID_NilCtx(t *testing.T) {
	// WithRequestID should accept any context.Context, including TODO
	ctx := WithRequestID(context.TODO(), "x")
	if got := RequestIDFromContext(ctx); got != "x" {
		t.Fatalf("got %q want %q", got, "x")
	}
}

func TestHeaderRequestID_Constant(t *testing.T) {
	if HeaderRequestID != "X-Request-ID" {
		t.Fatalf("HeaderRequestID = %q want X-Request-ID", HeaderRequestID)
	}
}
