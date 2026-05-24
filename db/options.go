package db

import (
	"log/slog"
	"time"
)

// Option configures Connect.
type Option func(*options)

type options struct {
	logger        *slog.Logger
	slowThreshold time.Duration
	metrics       *metricsCollector
}

// WithLogger wires a slog.Logger into the pgx QueryTracer. Without this
// option, queries are logged nowhere.
//
// Levels emitted by the tracer:
//   - Debug: every successful query (sql + elapsed). Off in prod by default.
//   - Warn:  queries slower than the slow-query threshold (see WithSlowQueryThreshold).
//   - Error: queries that returned an error.
func WithLogger(logger *slog.Logger) Option {
	return func(o *options) { o.logger = logger }
}

// WithSlowQueryThreshold sets the threshold above which queries are logged
// at Warn level (only effective alongside WithLogger). Default: 500ms.
func WithSlowQueryThreshold(d time.Duration) Option {
	return func(o *options) { o.slowThreshold = d }
}
