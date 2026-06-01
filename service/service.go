package service

import (
	"context"
	"log/slog"
	"net/http"
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/theizzatbek/gokit/auth"
	"github.com/theizzatbek/gokit/clients/apimap"
	natsclient "github.com/theizzatbek/gokit/clients/nats"
	"github.com/theizzatbek/gokit/clients/natsmap"
	"github.com/theizzatbek/gokit/clients/ratelimit"
	redisclient "github.com/theizzatbek/gokit/clients/redis"
	s3client "github.com/theizzatbek/gokit/clients/s3"
	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/db/outbox"
	"github.com/theizzatbek/gokit/fibermap"
)

// Service is the bundled runtime. Public fields are nil for subsystems
// that weren't opted into via Config; check before use.
type Service[T any, C any] struct {
	DB      *db.DB              // nil when Config.DB.User == ""
	Auth    *auth.Auth[C]       // nil when Config.Auth.PrivateKeyPEM == ""
	NATS    *natsclient.Client  // nil when Config.NATS.URL == ""
	Redis   *redisclient.Client // nil when Config.Redis.URL == ""
	NATSMap *natsmap.Runtime    // nil when Config.NATSMap.*Path is empty
	HTTPC   *http.Client        // always built
	APIMap  *apimap.Client      // nil when Config.APIMap.Path == ""
	Engine  *fibermap.Engine[T] // always built
	Hasher  *auth.Hasher        // nil when Auth is nil
	Outbox  *outbox.Worker      // nil unless WithOutbox + DB + NATSMap all wired
	S3      *s3client.Client    // nil when Config.S3.Bucket == ""

	// RateLimiter is the Redis-backed sliding-window limiter built by
	// [WithRateLimit]. nil unless the option was passed AND Redis is
	// configured. When non-nil, the `rate_limit_redis` YAML factory
	// is automatically registered on Engine.
	RateLimiter *ratelimit.Redis

	cfg     Config
	logger  *slog.Logger
	metrics prometheus.Registerer
	opts    *options

	shutdownMu  sync.Mutex
	shutdownFns []func() error

	// refreshStore is non-nil iff Auth was built. Held so WithRefreshGC
	// can call GarbageCollect on it without going through Auth (Auth's
	// internal store field is unexported and intentionally so).
	refreshStore auth.RefreshStore

	// otelShutdown is non-nil iff WithOtel was passed. Flushes
	// pending spans during Close.
	otelShutdown func(context.Context) error

	// otelMetricsShutdown is non-nil iff WithOtel was passed AND the
	// service registry implements prometheus.Gatherer (the default
	// does). Flushes the OTLP metric pipeline during Close.
	otelMetricsShutdown func(context.Context) error

	// otelLogsShutdown is non-nil iff WithOtel was passed AND
	// WithoutOtelLogs was not. Flushes the OTLP log pipeline.
	otelLogsShutdown func(context.Context) error

	// sentryShutdown is non-nil iff WithSentry was passed. Flushes
	// pending Sentry events during Close.
	sentryShutdown func(context.Context) error

	// scheduler is non-nil iff WithCron jobs were registered.
	scheduler *scheduler

	closed bool
}

// Logger returns the *slog.Logger Service constructed (or the one
// supplied via WithLogger).
func (s *Service[T, C]) Logger() *slog.Logger { return s.logger }

// Metrics returns the prometheus.Registerer Service constructed (or
// the one supplied via WithMetrics).
func (s *Service[T, C]) Metrics() prometheus.Registerer { return s.metrics }

// SetContextBuilder is the typed proxy for Engine.SetContextBuilder.
func (s *Service[T, C]) SetContextBuilder(fn fibermap.ContextBuilder[T]) {
	s.Engine.SetContextBuilder(fn)
}

// SetClaimsRefresher is the typed proxy for Auth.SetClaimsRefresher.
func (s *Service[T, C]) SetClaimsRefresher(r auth.ClaimsRefresher[C]) {
	if s.Auth == nil {
		panic("service: SetClaimsRefresher called but Config.Auth.PrivateKeyPEM is empty")
	}
	s.Auth.SetClaimsRefresher(r)
}

// OnShutdown registers a cleanup callback to run during [Service.Close],
// BEFORE the kit-managed subsystems (NATSMap drain, NATS close, DB
// close) are torn down — user code can still talk to the database, flush
// outbound queues, etc. Registered callbacks run in LIFO order so the
// teardown unwinds the construction order.
//
// Typical use: register cleanup for resources Service didn't build —
// app-specific workers, third-party clients, Prometheus pushers, etc.
//
//	worker := startWorker(svc.DB)
//	svc.OnShutdown(worker.Stop)
//
// Errors returned by the callback are logged via the service logger and
// do not stop subsequent callbacks. Calling OnShutdown after Close is a
// no-op (the callback is dropped without invocation).
//
// Thread-safe.
func (s *Service[T, C]) OnShutdown(fn func() error) {
	if fn == nil {
		return
	}
	s.shutdownMu.Lock()
	defer s.shutdownMu.Unlock()
	if s.closed {
		return
	}
	s.shutdownFns = append(s.shutdownFns, fn)
}
