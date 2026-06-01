package otelkit

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestSlogHandler_TeesToInner(t *testing.T) {
	// Inner handler writes JSON to a buffer; tee should propagate
	// every record to it regardless of OTel-side state.
	var buf bytes.Buffer
	inner := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})

	h := SlogHandler(inner, "test")
	logger := slog.New(h)
	logger.Info("hello", "x", 1)

	if !strings.Contains(buf.String(), `"msg":"hello"`) {
		t.Errorf("inner did not receive record: %s", buf.String())
	}
	if !strings.Contains(buf.String(), `"x":1`) {
		t.Errorf("inner did not receive attr: %s", buf.String())
	}
}

func TestSlogHandler_NilInnerFallsBackToDefault(t *testing.T) {
	// Should not panic when inner is nil.
	h := SlogHandler(nil, "test")
	logger := slog.New(h)
	logger.Info("ok")
}

func TestSlogHandler_WithAttrs(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewJSONHandler(&buf, nil)
	h := SlogHandler(inner, "test").WithAttrs([]slog.Attr{slog.String("svc", "test")})
	logger := slog.New(h)
	logger.Info("hello")
	if !strings.Contains(buf.String(), `"svc":"test"`) {
		t.Errorf("WithAttrs did not propagate to inner: %s", buf.String())
	}
}

func TestSlogHandler_WithGroup(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewJSONHandler(&buf, nil)
	h := SlogHandler(inner, "test").WithGroup("svc")
	logger := slog.New(h)
	logger.Info("hello", "k", "v")
	if !strings.Contains(buf.String(), `"svc":{"k":"v"}`) {
		t.Errorf("WithGroup did not propagate to inner: %s", buf.String())
	}
}

func TestSlogHandler_EnabledTrueWhenInnerEnabled(t *testing.T) {
	inner := slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelDebug})
	h := SlogHandler(inner, "test")
	if !h.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("tee should be Enabled at Info when inner accepts it")
	}
}

func TestSetupLogs_EmptyServiceNameErrors(t *testing.T) {
	if _, err := SetupLogs(context.Background(), ""); err == nil {
		t.Error("expected error for empty serviceName")
	}
}
