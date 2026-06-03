package cronmap

import (
	"context"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/robfig/cron/v3"
)

// HandlerFn is the registered job body. ctx carries the optional
// per-run timeout (when YAML `timeout:` is set) and the runtime-wide
// shutdown signal — handlers SHOULD honour ctx.Done() to exit early
// during Stop or timeout.
type HandlerFn func(ctx context.Context) error

// SingletonLocker is the cross-instance lock interface used by jobs
// marked `singleton: true` in YAML. [db/lock.Locker]'s shape matches
// this; alternative backends (Redis SETNX, etcd lease) can also
// implement it.
//
// TryLock semantics:
//
//   - ok=false (with err == nil): the lock is held by another
//     instance; the run is skipped. cronmap_singleton_skipped_total
//     increments.
//   - ok=true: the lock is acquired. The runtime invokes the
//     handler, then calls the returned release exactly once.
//   - err != nil: the locker backend itself failed (e.g. DB
//     connection lost). The run is skipped and the error is logged;
//     cronmap_runs_total{outcome=failure} increments.
type SingletonLocker interface {
	TryLock(ctx context.Context, key string) (release func(), ok bool, err error)
}

// EngineOption configures the engine at construction time. Today the
// only option is [WithEnv]; reserved for future construction-time
// knobs.
type EngineOption func(*Engine)

// WithEnv supplies explicit values for ${VAR} substitution at
// LoadBytes / LoadFile time. The map is consulted first; on miss the
// lookup falls back to os.LookupEnv. nil or empty map is a no-op
// (falls back to os.LookupEnv for every key).
func WithEnv(m map[string]string) EngineOption {
	return func(e *Engine) { e.envMap = m }
}

// BuildOption configures the runtime at [Engine.Build] time.
type BuildOption func(*buildOptions)

type buildOptions struct {
	parser         cron.Parser
	logger         *slog.Logger
	metrics        prometheus.Registerer
	locker         SingletonLocker
	useSentry      bool
	onTickStart    func(ctx context.Context, name string)
	onTickComplete func(ctx context.Context, name string, err error, elapsed time.Duration)
}

// defaultParser is the 5-field cron expression set (no seconds) plus
// the `@daily`/`@hourly` descriptor family. Matches
// service.WithCron's default so migration to cronmap doesn't change
// schedule semantics.
func defaultParser() cron.Parser {
	return cron.NewParser(
		cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor,
	)
}

// WithParser overrides the default 5-field cron parser. Pass a
// seconds-precision parser for tight tests or second-level cadences:
//
//	cronmap.WithParser(cron.NewParser(
//	    cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
//	))
func WithParser(p cron.Parser) BuildOption {
	return func(o *buildOptions) { o.parser = p }
}

// WithLogger wires a slog.Logger for scheduler lifecycle records,
// per-tick failures, and singleton-skip diagnostics. nil = silent.
func WithLogger(l *slog.Logger) BuildOption {
	return func(o *buildOptions) { o.logger = l }
}

// WithMetrics registers the kit's standard cronmap_* collectors on
// reg. nil = zero Prometheus footprint.
func WithMetrics(r prometheus.Registerer) BuildOption {
	return func(o *buildOptions) { o.metrics = r }
}

// WithSingletonLocker supplies the cross-instance lock backend used
// by jobs marked `singleton: true`. Build fails with
// [CodeSingletonNeedsLocker] when any job needs leader election but
// no locker was passed.
//
// [PGLocker] is the kit's default backend over db/lock; callers can
// pass any type satisfying [SingletonLocker].
func WithSingletonLocker(l SingletonLocker) BuildOption {
	return func(o *buildOptions) { o.locker = l }
}

// WithSentry enables sentrykit.MonitorCron wrapping for jobs. The
// per-job sentry_slug field (or slugified name as fallback) is
// passed to MonitorCron, which transparently no-ops when sentrykit
// itself was never initialised.
func WithSentry() BuildOption {
	return func(o *buildOptions) { o.useSentry = true }
}

// WithOnTickStart registers a callback fired BEFORE the handler runs
// (and before any retry loop). Use for tracing span starts, audit
// log begin, request_id stamping. Panic-safe — the kit recovers
// callback panics so a broken hook does not kill the tick.
func WithOnTickStart(fn func(ctx context.Context, name string)) BuildOption {
	return func(o *buildOptions) { o.onTickStart = fn }
}

// WithOnTickComplete registers a callback fired AFTER the handler
// (or final retry attempt) returns. err is nil on success;
// elapsed is the total wall-clock time spent across all attempts.
// Panic-safe — same convention as [WithOnTickStart].
func WithOnTickComplete(fn func(ctx context.Context, name string, err error, elapsed time.Duration)) BuildOption {
	return func(o *buildOptions) { o.onTickComplete = fn }
}
