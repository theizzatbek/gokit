package svckit

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/gofiber/fiber/v2"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/theizzatbek/gokit/clients/httpc"
	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/fibermap"
)

// Host is everything the core gives a mod. The interface is
// deliberately free of type parameters: a mod must not know either T
// (the engine's payload type) or C (claims). Where a generic is still
// needed — the YAML middleware factory and subject-based keying — the
// core hands back a non-generic slice instead.
type Host interface {
	// Logger — the service logger at call time. otelmod/sentrymod
	// may replace it via SetLogger during the Setup phase, and every
	// subsequent mod receives the already-wrapped one.
	Logger() *slog.Logger
	SetLogger(*slog.Logger)

	Metrics() prometheus.Registerer

	// DB — nil when Config.DB.User is empty. A mod must check this:
	// webhooks and outbox cannot be built without a DB.
	DB() *db.DB

	// HTTPC — the kit-built *http.Client (logger + metrics already
	// wired, plus anything a Setuper added via AddHTTPCOption). Use
	// this as the transport for outbound calls instead of building a
	// parallel client that bypasses the shared observability/breaker/
	// bulkhead stack — this is what webhooksmod delivers over.
	//
	// nil during the Setup phase (the core builds the client AFTER
	// Setup finishes — see AddHTTPCOption's doc for why). Build and
	// Wire always see the finished client.
	HTTPC() *http.Client

	// SubjectKeyFn — the non-generic slice of Auth: a rate-limit key
	// derived from the JWT subject. nil when Auth is not configured.
	SubjectKeyFn() func(*fiber.Ctx) string

	NodeName() string
	ServerGroup() string

	// Context — the service's long-lived runCtx, cancelled at the
	// head of [Service.Close] before the OnShutdown chain runs. Use
	// this to start a background worker that must outlive the Setup/
	// Build/Wire call that created it and observe shutdown via
	// ctx.Done() — the same ctx the core's own cron scheduler and
	// refresh-GC ticker run on. Do NOT substitute the ctx passed into
	// Setup/Build (that one is scoped to boot and may be cancelled by
	// the caller right after [New] returns without meaning to stop
	// background work).
	Context() context.Context

	// ResolvePath — the same logic the core uses for routes.yaml: an
	// explicit path wins, otherwise defaultName inside ConfigsDir,
	// otherwise empty (subsystem disabled).
	ResolvePath(userPath, defaultName string, enabled bool) string

	// AddHTTPCOption affects the HTTP client the core builds after
	// the Setup phase. Calling this from Build is already too late.
	AddHTTPCOption(...httpc.Option)

	// UseFiber — app-level middleware, outside the engine.
	UseFiber(...fiber.Handler)

	// AddRunOption — anything that ends up in Engine.Run: mounting
	// routes, e.g. an HTTP→NATS gateway.
	AddRunOption(...fibermap.RunOption)

	AddReadinessChecker(...fibermap.Checker)

	// RegisterMiddlewareFactory — a YAML middleware factory under the
	// name name. The core adapts it to the engine's type. Callable
	// from any phase (Setup, Build, or Wire) — the core collects
	// registrations after Build and again after Wire, so a mod that
	// needs the engine to exist first (v1's rate_limit_redis mounts
	// after buildEngine) can still reach it from Wire.
	RegisterMiddlewareFactory(name string, f FactoryFunc)

	// WrapCronJob registers a decorator applied around every tick the
	// core schedules — [WithCron]/[WithSingletonCron] entries built at
	// New time, [Service.AddCron]/[Service.AddSingletonCron]
	// registered afterwards, and the refresh-GC ticker. Decorators
	// apply in registration order, each wrapping the previous result
	// (`fn = w(fn)`), so the last-registered decorator runs outermost
	// — the usual middleware-chaining convention. v1 ran every one of
	// these ticks through sentrykit.MonitorCronWithConfig; cron lives
	// in the core and the core can't depend on Sentry, so this hook
	// is how sentrymod restores that monitoring without the core
	// knowing what Sentry is. nil is ignored.
	WrapCronJob(func(JobFn) JobFn)

	// OnShutdown — resource cleanup, LIFO relative to registration
	// order. nil is ignored.
	OnShutdown(func() error)
}

// hostImpl is the only implementation of Host. It lives exactly as
// long as New runs: a mod must not hold onto it, because once the
// build finishes, the accumulators it writes to have already been
// consumed.
type hostImpl struct {
	opts        *options
	cfg         Config
	logger      *slog.Logger
	metrics     prometheus.Registerer
	db          *db.DB
	httpc       *http.Client
	subjectKey  func(*fiber.Ctx) string
	nodeName    string
	serverGroup string
	ctx         context.Context
}

func (h *hostImpl) Logger() *slog.Logger           { return h.logger }
func (h *hostImpl) SetLogger(l *slog.Logger)       { h.logger = l }
func (h *hostImpl) Metrics() prometheus.Registerer { return h.metrics }
func (h *hostImpl) DB() *db.DB                     { return h.db }
func (h *hostImpl) HTTPC() *http.Client            { return h.httpc }
func (h *hostImpl) NodeName() string               { return h.nodeName }
func (h *hostImpl) ServerGroup() string            { return h.serverGroup }
func (h *hostImpl) Context() context.Context       { return h.ctx }

func (h *hostImpl) SubjectKeyFn() func(*fiber.Ctx) string { return h.subjectKey }

func (h *hostImpl) ResolvePath(userPath, defaultName string, enabled bool) string {
	return resolvePathInDir(h.cfg.Service.ConfigsDir, userPath, defaultName, enabled)
}

func (h *hostImpl) AddHTTPCOption(o ...httpc.Option) {
	h.opts.httpcOpts = append(h.opts.httpcOpts, o...)
}

func (h *hostImpl) UseFiber(mw ...fiber.Handler) {
	h.opts.fiberMiddleware = append(h.opts.fiberMiddleware, mw...)
}

func (h *hostImpl) AddRunOption(o ...fibermap.RunOption) {
	h.opts.runOpts = append(h.opts.runOpts, o...)
}

func (h *hostImpl) AddReadinessChecker(c ...fibermap.Checker) {
	h.opts.readinessCheckers = append(h.opts.readinessCheckers, c...)
}

func (h *hostImpl) RegisterMiddlewareFactory(name string, f FactoryFunc) {
	if h.opts.modFactories == nil {
		h.opts.modFactories = map[string]FactoryFunc{}
	}
	if _, dup := h.opts.modFactories[name]; dup && h.logger != nil {
		// Two mods (or the same mod twice) claimed the same factory
		// name. Unlike validateMods (mod names, caught before any
		// phase runs) there's no equivalent up-front check here — the
		// collision only exists once two Setup/Build/Wire calls have
		// both landed on this map — so the best the core can do is
		// warn loudly and keep last-write-wins, matching what the map
		// assignment below does regardless.
		h.logger.Warn("svckit: middleware factory name registered more than once — last registration wins",
			"name", name)
	}
	h.opts.modFactories[name] = f
}

func (h *hostImpl) WrapCronJob(w func(JobFn) JobFn) {
	if w == nil {
		return
	}
	h.opts.cronWrappers = append(h.opts.cronWrappers, w)
}

func (h *hostImpl) OnShutdown(fn func() error) {
	if fn == nil {
		return
	}
	h.opts.shutdownFns = append(h.opts.shutdownFns, fn)
}
