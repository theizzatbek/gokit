package reqctx

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestSlogHandler_InjectsRequestID(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewJSONHandler(&buf, nil)
	l := slog.New(SlogHandler(inner))
	ctx := WithRequestID(context.Background(), "abc-123")
	l.InfoContext(ctx, "hello")
	if !strings.Contains(buf.String(), `"request_id":"abc-123"`) {
		t.Fatalf("expected request_id in output: %s", buf.String())
	}
}

func TestSlogHandler_AbsentWhenCtxEmpty(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewJSONHandler(&buf, nil)
	l := slog.New(SlogHandler(inner))
	l.InfoContext(context.Background(), "hello")
	if strings.Contains(buf.String(), "request_id") {
		t.Fatalf("did not expect request_id attr: %s", buf.String())
	}
}

func TestSlogHandler_NotDoubleWrapped(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewJSONHandler(&buf, nil)
	once := SlogHandler(inner)
	twice := SlogHandler(once)
	if once != twice {
		t.Fatalf("SlogHandler should be idempotent on its own wrap; got new pointer")
	}
}

func TestSlogHandler_RecordAlreadyHasAttr_NoDup(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewJSONHandler(&buf, nil)
	l := slog.New(SlogHandler(inner))
	ctx := WithRequestID(context.Background(), "from-ctx")
	l.InfoContext(ctx, "hello", "request_id", "from-call")
	out := buf.String()
	// Caller-supplied attr should win (slog json handler keeps later occurrences;
	// our wrapper does NOT add duplicate when an explicit attr is present).
	if strings.Count(out, "request_id") != 1 {
		t.Fatalf("expected exactly one request_id attr, got: %s", out)
	}
	if !strings.Contains(out, `"request_id":"from-call"`) {
		t.Fatalf("expected caller-supplied request_id, got: %s", out)
	}
}

func TestSlogHandler_WithAttrs_PreservesWrap(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewJSONHandler(&buf, nil)
	l := slog.New(SlogHandler(inner)).With("svc", "test")
	ctx := WithRequestID(context.Background(), "rid")
	l.InfoContext(ctx, "hello")
	if !strings.Contains(buf.String(), `"request_id":"rid"`) {
		t.Fatalf("WithAttrs broke wrap: %s", buf.String())
	}
	if !strings.Contains(buf.String(), `"svc":"test"`) {
		t.Fatalf("WithAttrs lost attr: %s", buf.String())
	}
}
