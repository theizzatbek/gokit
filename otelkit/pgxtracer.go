package otelkit

import (
	"context"

	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// PgxTracer is a minimal pgx.QueryTracer that opens an OTel CLIENT
// span per query, records the statement text, and ends the span with
// the appropriate status code based on the query's error.
//
// Use via [db.WithTracer] (or let [service.WithOtel] auto-wire it for
// you). The tracer reads the named tracer from otel.Tracer at
// construction time so installing a TracerProvider AFTER NewPgxTracer
// still works.
//
//	pgxTracer := otelkit.NewPgxTracer(otelkit.WithPgxTracerName("orders-db"))
//	dbConn, _ := db.Connect(ctx, cfg, db.WithTracer(pgxTracer))
//
// The span name is "db.query"; switch to operation-aware naming via
// [WithPgxSpanNamer] when downstream tooling expects "SELECT users"
// style names. The default keeps the span name PII-free and
// cardinality-low.
type PgxTracer struct {
	tracer       trace.Tracer
	spanNamer    func(sql string) string
	recordSQL    bool
	maxSQLLength int
}

// PgxTracerOption configures [NewPgxTracer].
type PgxTracerOption func(*PgxTracer)

// WithPgxTracerName overrides the OTel tracer name. Defaults to
// "github.com/theizzatbek/gokit/otelkit". The tracer name appears in
// instrumentation library metadata on every span; set per service to
// distinguish multiple DBs sharing one collector.
func WithPgxTracerName(name string) PgxTracerOption {
	return func(t *PgxTracer) { t.tracer = otel.Tracer(name) }
}

// WithPgxSpanNamer overrides the span-name builder. Default returns
// "db.query" regardless of SQL. Pass a function that derives a name
// from the statement when downstream tooling (Jaeger/Tempo) expects
// human-readable span names.
//
//	otelkit.WithPgxSpanNamer(func(sql string) string {
//	    if op, _, _ := strings.Cut(sql, " "); op != "" {
//	        return "db." + strings.ToLower(op)
//	    }
//	    return "db.query"
//	})
//
// Be careful — span names are unbounded-cardinality if you embed
// parameters. Keep the function deterministic on SQL shape only.
func WithPgxSpanNamer(fn func(sql string) string) PgxTracerOption {
	return func(t *PgxTracer) { t.spanNamer = fn }
}

// WithoutPgxSQL suppresses the db.statement attribute on every span.
// Use when the SQL itself is considered sensitive (multi-tenant
// service where queries embed tenant predicates, audit-compliance
// constraint, etc.). Default-on because SQL is the single most useful
// signal in a DB trace.
func WithoutPgxSQL() PgxTracerOption {
	return func(t *PgxTracer) { t.recordSQL = false }
}

// WithPgxMaxSQLLength truncates the db.statement attribute at the
// given byte length. Default 4096 — enough for the long
// JOIN-with-CTE queries kit services tend to write, not enough to
// blow span size budgets. 0 disables truncation; negative is treated
// as default.
func WithPgxMaxSQLLength(n int) PgxTracerOption {
	return func(t *PgxTracer) {
		if n < 0 {
			n = defaultPgxMaxSQLLength
		}
		t.maxSQLLength = n
	}
}

const (
	defaultPgxTracerName   = "github.com/theizzatbek/gokit/otelkit"
	defaultPgxMaxSQLLength = 4096
	defaultPgxSpanName     = "db.query"
)

// NewPgxTracer builds a PgxTracer with the supplied options.
// Implements pgx.QueryTracer.
func NewPgxTracer(opts ...PgxTracerOption) *PgxTracer {
	t := &PgxTracer{
		tracer:       otel.Tracer(defaultPgxTracerName),
		spanNamer:    func(string) string { return defaultPgxSpanName },
		recordSQL:    true,
		maxSQLLength: defaultPgxMaxSQLLength,
	}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

type pgxSpanKey struct{}

// TraceQueryStart opens a CLIENT span on the supplied ctx. The span
// is later closed by TraceQueryEnd via the per-query context returned
// here. The span carries db.system=postgresql + db.statement (when
// not suppressed) at start; status is recorded at end.
func (t *PgxTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	name := t.spanNamer(data.SQL)
	attrs := []attribute.KeyValue{
		semconv.DBSystemPostgreSQL,
	}
	if t.recordSQL {
		stmt := data.SQL
		if t.maxSQLLength > 0 && len(stmt) > t.maxSQLLength {
			stmt = stmt[:t.maxSQLLength]
		}
		attrs = append(attrs, semconv.DBQueryText(stmt))
	}
	ctx, span := t.tracer.Start(ctx, name,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attrs...),
	)
	return context.WithValue(ctx, pgxSpanKey{}, span)
}

// TraceQueryEnd closes the per-query span. data.Err is reflected on
// the span: non-nil → RecordError + Status(Error); nil → no status
// change (default Unset). Span affected-rows count is attached when
// data.CommandTag carries a row count.
func (t *PgxTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	span, _ := ctx.Value(pgxSpanKey{}).(trace.Span)
	if span == nil {
		return
	}
	if rows := data.CommandTag.RowsAffected(); rows > 0 {
		span.SetAttributes(attribute.Int64("db.rows_affected", rows))
	}
	if data.Err != nil {
		span.RecordError(data.Err)
		span.SetStatus(codes.Error, data.Err.Error())
	}
	span.End()
}
