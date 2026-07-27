package svckit

import (
	"context"
	"log/slog"
	"net/http"
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/theizzatbek/gokit/auth"
	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/fibermap"
)

// Service is the assembled runtime. Optional subsystems do not live
// here: their handles are held by the mods themselves.
type Service[T any, C any] struct {
	DB     *db.DB              // nil when Config.DB.User is empty
	Auth   *auth.Auth[C]       // nil when Config.Auth.PrivateKeyPEM is empty
	Hasher *auth.Hasher        // nil when Auth is nil
	HTTPC  *http.Client        // always
	Engine *fibermap.Engine[T] // always

	cfg     Config
	logger  *slog.Logger
	metrics prometheus.Registerer
	opts    *options

	mods    []Mod       // in connect order
	modStat []ModStatus // snapshot taken once at the end of New

	shutdownMu  sync.Mutex
	shutdownFns []func() error

	// refreshStore is non-nil iff Auth was built: WithRefreshGC calls
	// GarbageCollect on it directly.
	refreshStore auth.RefreshStore

	// scheduler is non-nil when WithCron jobs were registered.
	scheduler *scheduler

	// devToolsMounted guards mountDevTools against running twice — see
	// that method's doc.
	devToolsMounted bool

	// runCtx is the long-lived context for background workers.
	// Cancelled at the head of Close, before the OnShutdown chain.
	runCtx    context.Context
	runCancel context.CancelFunc

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
		panic("svckit: SetClaimsRefresher called but Config.Auth.PrivateKeyPEM is empty")
	}
	s.Auth.SetClaimsRefresher(r)
}

// OnShutdown registers a cleanup callback to run during [Service.Close],
// BEFORE the kit-managed subsystems (DB drain) are torn down — user
// code can still talk to the database, flush outbound queues, etc.
// Registered callbacks run in LIFO order so the teardown unwinds the
// construction order.
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
