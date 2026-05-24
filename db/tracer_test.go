package db

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestTracer_PicksErrorLevelOnFailure(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	tr := &tracer{logger: logger, slowThreshold: 500 * time.Millisecond}

	ctx := tr.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{SQL: "SELECT 1"})
	tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{Err: errors.New("boom")})

	out := buf.String()
	if !strings.Contains(out, "level=ERROR") {
		t.Fatalf("expected ERROR level, got %q", out)
	}
}

func TestTracer_PicksWarnLevelOnSlowQuery(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	tr := &tracer{logger: logger, slowThreshold: 1 * time.Nanosecond}

	ctx := tr.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{SQL: "SELECT 1"})
	time.Sleep(2 * time.Millisecond)
	tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{})

	if !strings.Contains(buf.String(), "level=WARN") {
		t.Fatalf("expected WARN level, got %q", buf.String())
	}
}

func TestTracer_PicksDebugLevelOnFastQuery(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	tr := &tracer{logger: logger, slowThreshold: 1 * time.Hour}

	ctx := tr.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{SQL: "SELECT 1"})
	tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{})

	if !strings.Contains(buf.String(), "level=DEBUG") {
		t.Fatalf("expected DEBUG level, got %q", buf.String())
	}
}

func TestTracer_TruncatesLongSQL(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	tr := &tracer{logger: logger, slowThreshold: 1 * time.Hour}

	long := strings.Repeat("x", 500)
	ctx := tr.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{SQL: long})
	tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{})

	if strings.Count(buf.String(), "x") > 210 {
		t.Fatalf("SQL was not truncated: %q", buf.String())
	}
}
