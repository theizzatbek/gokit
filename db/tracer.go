package db

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
)

const maxLoggedSQLChars = 200

type tracer struct {
	logger        *slog.Logger
	metrics       *metricsCollector
	slowThreshold time.Duration
}

type tracerCtxKey struct{}

type tracerSpan struct {
	start time.Time
	sql   string
}

func (t *tracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, tracerCtxKey{}, &tracerSpan{start: time.Now(), sql: data.SQL})
}

func (t *tracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	span, _ := ctx.Value(tracerCtxKey{}).(*tracerSpan)
	if span == nil {
		return
	}
	elapsed := time.Since(span.start)

	if t.metrics != nil {
		t.metrics.observe(elapsed, data.Err)
	}

	slow := data.Err == nil && t.slowThreshold > 0 && elapsed > t.slowThreshold
	if slow && t.metrics != nil {
		t.metrics.incSlowQuery()
	}

	if t.logger == nil {
		return
	}
	level := slog.LevelDebug
	switch {
	case data.Err != nil:
		level = slog.LevelError
	case slow:
		level = slog.LevelWarn
	}
	sql := span.sql
	if len(sql) > maxLoggedSQLChars {
		sql = sql[:maxLoggedSQLChars]
	}
	attrs := []slog.Attr{
		slog.String("sql", sql),
		slog.Duration("elapsed", elapsed),
	}
	if data.Err != nil {
		attrs = append(attrs, slog.Any("err", mapPgxErr(data.Err)))
	}
	t.logger.LogAttrs(ctx, level, "db query", attrs...)
}
