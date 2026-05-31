package otelkit

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// installRecorder swaps the global TracerProvider with a fresh one
// that records every span into the returned recorder. t.Cleanup
// restores nothing — tests must construct their PgxTracer AFTER this
// call so otel.Tracer() returns one bound to the recorder.
func installRecorder(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	prev := otel.GetTracerProvider()
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { otel.SetTracerProvider(prev) })
	return rec
}

func TestPgxTracer_OpensClientSpanWithSQL(t *testing.T) {
	rec := installRecorder(t)
	tr := NewPgxTracer()

	ctx := tr.TraceQueryStart(context.Background(), nil,
		pgx.TraceQueryStartData{SQL: "SELECT id FROM users"})
	tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{})

	spans := rec.Ended()
	if len(spans) != 1 {
		t.Fatalf("len(spans) = %d, want 1", len(spans))
	}
	s := spans[0]
	if s.Name() != defaultPgxSpanName {
		t.Errorf("span name = %q, want %q", s.Name(), defaultPgxSpanName)
	}
	found := false
	for _, a := range s.Attributes() {
		if string(a.Key) == "db.query.text" && strings.Contains(a.Value.AsString(), "SELECT id FROM users") {
			found = true
		}
	}
	if !found {
		t.Errorf("db.query.text attr missing; attrs = %+v", s.Attributes())
	}
}

func TestPgxTracer_RecordsErrorOnFailedQuery(t *testing.T) {
	rec := installRecorder(t)
	tr := NewPgxTracer()

	ctx := tr.TraceQueryStart(context.Background(), nil,
		pgx.TraceQueryStartData{SQL: "BAD SQL"})
	tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{Err: errors.New("syntax error")})

	spans := rec.Ended()
	if len(spans) != 1 {
		t.Fatalf("len(spans) = %d, want 1", len(spans))
	}
	if spans[0].Status().Code != codes.Error {
		t.Errorf("status = %v, want Error", spans[0].Status())
	}
	if !strings.Contains(spans[0].Status().Description, "syntax error") {
		t.Errorf("status.Description = %q, want contains 'syntax error'", spans[0].Status().Description)
	}
}

func TestPgxTracer_RecordsRowsAffected(t *testing.T) {
	rec := installRecorder(t)
	tr := NewPgxTracer()

	ctx := tr.TraceQueryStart(context.Background(), nil,
		pgx.TraceQueryStartData{SQL: "UPDATE links SET visit_count = visit_count + 1"})
	tag := pgconn.NewCommandTag("UPDATE 42")
	tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{CommandTag: tag})

	spans := rec.Ended()
	if len(spans) != 1 {
		t.Fatalf("len(spans) = %d, want 1", len(spans))
	}
	var rows int64
	for _, a := range spans[0].Attributes() {
		if string(a.Key) == "db.rows_affected" {
			rows = a.Value.AsInt64()
		}
	}
	if rows != 42 {
		t.Errorf("db.rows_affected = %d, want 42", rows)
	}
}

func TestPgxTracer_WithoutPgxSQL_SuppressesStatementAttr(t *testing.T) {
	rec := installRecorder(t)
	tr := NewPgxTracer(WithoutPgxSQL())

	ctx := tr.TraceQueryStart(context.Background(), nil,
		pgx.TraceQueryStartData{SQL: "SELECT secret_token FROM api_keys"})
	tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{})

	spans := rec.Ended()
	for _, a := range spans[0].Attributes() {
		if string(a.Key) == "db.query.text" {
			t.Errorf("db.query.text should be suppressed; got %v", a.Value.AsString())
		}
	}
}

func TestPgxTracer_TruncatesLongSQL(t *testing.T) {
	rec := installRecorder(t)
	tr := NewPgxTracer(WithPgxMaxSQLLength(10))

	ctx := tr.TraceQueryStart(context.Background(), nil,
		pgx.TraceQueryStartData{SQL: "SELECT 1234567890 FROM dual"})
	tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{})

	spans := rec.Ended()
	for _, a := range spans[0].Attributes() {
		if string(a.Key) == "db.query.text" {
			if got := a.Value.AsString(); len(got) != 10 {
				t.Errorf("len(db.query.text) = %d, want 10", len(got))
			}
		}
	}
}

func TestPgxTracer_CustomSpanNamer(t *testing.T) {
	rec := installRecorder(t)
	tr := NewPgxTracer(WithPgxSpanNamer(func(sql string) string {
		op, _, _ := strings.Cut(sql, " ")
		return "db." + strings.ToLower(op)
	}))

	ctx := tr.TraceQueryStart(context.Background(), nil,
		pgx.TraceQueryStartData{SQL: "INSERT INTO links (...) VALUES (...)"})
	tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{})

	spans := rec.Ended()
	if got := spans[0].Name(); got != "db.insert" {
		t.Errorf("span name = %q, want db.insert", got)
	}
}
