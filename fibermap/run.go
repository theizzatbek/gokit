package fibermap

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/prometheus/client_golang/prometheus"
)

// RunOption configures Engine.Run. Default behaviour with zero options:
//
//   - Create a fresh fiber.App via fiber.New().
//   - Load routes from "routes.yaml" on disk (skipped if the engine
//     already loaded a YAML document).
//   - Mount on the new app.
//   - Listen on $PORT if set ("PORT=8080" → ":8080"), else ":3000".
//   - On SIGINT/SIGTERM, gracefully drain in-flight requests for up to
//     10s, then exit.
type RunOption func(*runConfig)

type runConfig struct {
	addr            string
	routesPath      string
	routesFS        fs.FS
	fiberConfig     *fiber.Config
	uses            []fiber.Handler
	configureApp    func(*fiber.App)
	shutdownTimeout time.Duration
	disableSignals  bool

	withRecover bool
	recoverLog  *slog.Logger
	noRecover   bool

	healthCheckPath string
	healthCheckSet  bool
	noHealthCheck   bool

	readinessPath     string
	readinessCheckers []Checker
	readinessOpts     []ReadinessOption

	withReqLog       bool
	reqLog           *slog.Logger
	reqLogSkipPaths  []string
	reqLogSlowThresh time.Duration
	noReqLog         bool

	noRequestID bool

	metricsPath     string
	metricsSet      bool
	noMetrics       bool
	metricsReg      prometheus.Registerer
	metricsGatherer prometheus.Gatherer

	// New middlewares (all opt-in).
	corsCfg            *CORSConfig
	rateLimitRPS       float64
	rateLimitBurst     int
	rateLimitSkip      []string
	bodyLimit          int
	compressionLevel   CompressionLevel
	compressionEnabled bool
	trustedProxies     []string
	notFoundHandler    fiber.Handler
}

// WithAddr overrides the listen address. When unset, Run picks up the
// `PORT` environment variable (cloud-platform convention: Heroku,
// Cloud Run, fly.io, Railway) and listens on `:${PORT}`; if `PORT` is
// also unset, defaults to ":3000". WithAddr always wins over `PORT`.
func WithAddr(addr string) RunOption {
	return func(c *runConfig) { c.addr = addr }
}

// WithRoutesPath overrides the YAML filename Run loads. Default
// "routes.yaml". Ignored if the engine already loaded a YAML
// document via LoadFile / LoadBytes / LoadFS before Run.
func WithRoutesPath(path string) RunOption {
	return func(c *runConfig) { c.routesPath = path }
}

// WithRoutesFS makes Run resolve RoutesPath via this fs.FS — typically
// an embed.FS so the YAML ships inside the binary:
//
//	//go:embed routes.yaml
//	var routesFS embed.FS
//
//	eng.Run(fibermap.WithRoutesFS(routesFS))
//
// Ignored if the engine already loaded a YAML document.
func WithRoutesFS(fsys fs.FS) RunOption {
	return func(c *runConfig) { c.routesFS = fsys }
}

// WithFiberConfig customizes fiber.New's argument. If not set, fiber.New()
// is called with no config.
func WithFiberConfig(cfg fiber.Config) RunOption {
	return func(c *runConfig) { c.fiberConfig = &cfg }
}

// WithUse installs Fiber-level middlewares via app.Use BEFORE the
// engine mounts. Use this for auth / request-id / logging that must
// run before fibermap's ContextBuilder (so locals are populated when
// the builder reads them).
//
// Can be passed multiple times; handlers are concatenated in order.
func WithUse(handlers ...fiber.Handler) RunOption {
	return func(c *runConfig) { c.uses = append(c.uses, handlers...) }
}

// WithConfigureApp is the escape hatch: a callback that gets the
// freshly-created *fiber.App after WithUse has been applied but
// before the engine mounts. Use it for anything WithUse can't
// express (groups, route-level handlers outside fibermap, ETag
// middleware, …).
func WithConfigureApp(fn func(*fiber.App)) RunOption {
	return func(c *runConfig) { c.configureApp = fn }
}

// WithShutdownTimeout overrides how long graceful shutdown waits for
// in-flight requests. Default 10s. Pass 0 or negative to disable
// graceful shutdown — Run then exits immediately on signal (or only
// when Listen returns).
func WithShutdownTimeout(d time.Duration) RunOption {
	return func(c *runConfig) { c.shutdownTimeout = d }
}

// WithoutSignalHandling disables Run's SIGINT/SIGTERM trap entirely.
// Use when embedding Run in a parent that owns process signals.
func WithoutSignalHandling() RunOption {
	return func(c *runConfig) { c.disableSignals = true }
}

// WithRecover installs [Recover] as the FIRST Fiber-level middleware
// (before WithUse handlers). Panics in any downstream middleware or
// handler are logged with the request's method, path, request_id,
// and a full stack trace via the given logger, and the client gets a
// generic 500 instead of a dropped connection. Pass nil for
// slog.Default().
//
// Recover is ON BY DEFAULT in Run — call WithRecover only to supply
// a custom logger, or WithoutRecover to disable.
func WithRecover(logger *slog.Logger) RunOption {
	return func(c *runConfig) {
		c.withRecover = true
		c.recoverLog = logger
		c.noRecover = false
	}
}

// WithoutRecover disables the built-in Recover middleware that Run
// otherwise installs by default. Use only when you have your own
// panic-handling wired (e.g. via Fiber's ErrorHandler or
// WithConfigureApp).
func WithoutRecover() RunOption {
	return func(c *runConfig) {
		c.noRecover = true
		c.withRecover = false
	}
}

// WithoutRequestID suppresses the built-in [RequestID] middleware
// that Run prepends to the Use chain by default. Use when you have
// a custom request-correlation scheme you're installing yourself via
// WithUse.
func WithoutRequestID() RunOption {
	return func(c *runConfig) { c.noRequestID = true }
}

// WithRequestLogger installs [RequestLogger] in the Use chain AFTER
// WithRecover (so panics still get recovered and logged separately).
// Skip paths are typically `/healthz` and `/metrics` — pass them via
// `skipPaths`. Empty (or nil) logger falls back to slog.Default.
//
// RequestLogger is ON BY DEFAULT in Run (with `slog.Default()` and
// skipPaths `[/healthz, /metrics]`) — call this only to supply a
// custom logger or skip-set, or WithoutRequestLogger to disable.
func WithRequestLogger(logger *slog.Logger, skipPaths ...string) RunOption {
	return func(c *runConfig) {
		c.withReqLog = true
		c.reqLog = logger
		c.reqLogSkipPaths = skipPaths
		c.noReqLog = false
	}
}

// WithoutRequestLogger disables the built-in [RequestLogger] that
// Run otherwise installs by default. Use when you log access yourself
// (e.g. via a different middleware in WithUse) and don't want a
// duplicate line.
func WithoutRequestLogger() RunOption {
	return func(c *runConfig) {
		c.noReqLog = true
		c.withReqLog = false
	}
}

// WithMetrics installs the Prometheus metrics middleware from [Metrics]
// and exposes the metrics at `path` (Prometheus text format). Default
// path "/metrics"; pass empty string to disable explicitly.
//
// The middleware is registered AFTER WithRecover / WithRequestLogger
// so panics and request logs still happen as usual, but counts of
// served requests reflect what actually succeeded.
//
// Metrics is OFF BY DEFAULT in Run (it pulls
// `github.com/prometheus/client_golang` into the binary). Use
// [Default] or call WithMetrics explicitly to enable it.
//
//	eng.Run(fibermap.WithMetrics("/metrics"))
func WithMetrics(path string) RunOption {
	return func(c *runConfig) {
		c.metricsPath = path
		c.metricsSet = true
		c.noMetrics = false
	}
}

// WithoutMetrics suppresses the Prometheus metrics endpoint. Useful
// in combination with [Default] when you want every default EXCEPT
// metrics.
func WithoutMetrics() RunOption {
	return func(c *runConfig) {
		c.noMetrics = true
		c.metricsPath = ""
	}
}

// MetricsRegistry is the registry shape WithMetricsRegistry accepts —
// both [prometheus.Registerer] (so fibermap can register its three
// HTTP collectors) and [prometheus.Gatherer] (so the scrape handler
// can serialise the current values). *prometheus.Registry satisfies
// both.
type MetricsRegistry interface {
	prometheus.Registerer
	prometheus.Gatherer
}

// WithMetricsRegistry routes the fibermap HTTP middleware metrics
// AND the /metrics scrape endpoint through the caller-provided
// registry instead of a private one created by [Metrics]. Use this to
// unify the kit's per-subsystem collectors (db, httpc, nats, …) so a
// single scrape returns the full picture:
//
//	reg := prometheus.NewRegistry()
//	db.WithMetrics(reg) // applied during db.Connect
//	httpc.WithMetrics(reg)
//	eng.Run(fibermap.WithMetricsRegistry(reg))
//
// Pairs with the [service] subpackage, which auto-applies its own
// registry to every subsystem and passes it to Run via this option.
//
// Has no effect on its own — [WithMetrics] (or [Default]) must also
// be in play to install the middleware + endpoint at all. Implicitly
// enables WithMetrics with path "/metrics" if neither WithMetrics nor
// WithoutMetrics was set.
func WithMetricsRegistry(reg MetricsRegistry) RunOption {
	return func(c *runConfig) {
		c.metricsReg = reg
		c.metricsGatherer = reg
		if !c.metricsSet {
			c.metricsPath = "/metrics"
			c.metricsSet = true
			c.noMetrics = false
		}
	}
}

// WithHealthCheck registers a `GET` handler at `path` returning
// 200 OK with body "ok". The route is installed BEFORE any other
// middleware (WithRecover, WithUse, ContextBuilder) so it is not
// subject to auth, context construction, or panic-bound code paths
// — exactly what you want for a k8s livenessProbe / readinessProbe.
//
// The endpoint does NOT appear in Engine.Routes() because it lives
// outside the engine's planned route set.
//
// HealthCheck is ON BY DEFAULT in Run at path `/healthz` — call
// WithHealthCheck only to move it (e.g. `WithHealthCheck("/_health")`),
// pass empty string to disable, or use [WithoutHealthCheck].
func WithHealthCheck(path string) RunOption {
	return func(c *runConfig) {
		c.healthCheckPath = path
		c.healthCheckSet = true
		if path == "" {
			c.noHealthCheck = true
		} else {
			c.noHealthCheck = false
		}
	}
}

// WithReadiness installs a readiness probe at `path` that runs the
// supplied [Checker] set in parallel and returns 200 with
// `{"status":"ok"}` if all pass OR 503 with
// `{"status":"degraded","checks":{...}}` if any fail. Like
// /healthz the route is wired BEFORE any middleware so it isn't
// blocked by auth, recover, or any user-installed Use chain — K8s
// readiness probes always reach the kit's check logic.
//
// Off by default at the fibermap layer; service.New auto-installs
// it from the wired subsystem set (DB, NATS, Redis) at `/readyz`.
// Customise the per-probe timeout via [WithReadinessTimeout].
//
//	eng.Run(fibermap.WithReadiness("/readyz", svc.ReadinessCheckers()...))
func WithReadiness(path string, checkers ...Checker) RunOption {
	return func(c *runConfig) {
		c.readinessPath = path
		c.readinessCheckers = checkers
	}
}

// WithReadinessOpts forwards [ReadinessOption] values to the
// readiness handler installed by [WithReadiness]. No-op when
// WithReadiness was not also passed.
func WithReadinessOpts(opts ...ReadinessOption) RunOption {
	return func(c *runConfig) { c.readinessOpts = append(c.readinessOpts, opts...) }
}

// WithoutHealthCheck disables the built-in health-check route that
// Run otherwise installs by default. Equivalent to
// `WithHealthCheck("")`.
func WithoutHealthCheck() RunOption {
	return func(c *runConfig) {
		c.noHealthCheck = true
		c.healthCheckSet = true
		c.healthCheckPath = ""
	}
}

// WithCORS installs the [CORS] middleware on the App level (BEFORE
// WithUse handlers). Pass a zero CORSConfig for kit defaults (allow
// any origin, common methods/headers).
func WithCORS(cfg ...CORSConfig) RunOption {
	return func(c *runConfig) {
		final := CORSConfig{}
		if len(cfg) > 0 {
			final = cfg[0]
		}
		c.corsCfg = &final
	}
}

// WithRateLimit installs an in-process IP-keyed token-bucket rate
// limiter at the App level. rps is sustained requests per second per
// IP; burst is the bucket size. skipPaths defaults to
// `/healthz, /readyz, /metrics` so k8s probes always pass.
//
// In-process limit — for multi-replica deployments use the
// fiber/middleware/limiter Storage pattern with Redis backing via
// [WithUse].
func WithRateLimit(rps float64, burst int, skipPaths ...string) RunOption {
	return func(c *runConfig) {
		c.rateLimitRPS = rps
		c.rateLimitBurst = burst
		if len(skipPaths) > 0 {
			c.rateLimitSkip = skipPaths
		}
	}
}

// WithBodyLimit installs a Fiber-level body-size cap. Requests with
// Content-Length > maxBytes (or a body that exceeds it mid-read)
// surface as 413 Request Entity Too Large.
//
// Sets fiber.Config.BodyLimit so the cap fires inside Fiber's parser
// BEFORE the body reaches the handler — defence-in-depth against
// huge-upload attacks.
func WithBodyLimit(maxBytes int) RunOption {
	return func(c *runConfig) { c.bodyLimit = maxBytes }
}

// WithCompression installs gzip/deflate response compression based
// on the request's Accept-Encoding header. Default level is
// CompressionBestSpeed — minimises CPU at modest size cost.
func WithCompression(level ...CompressionLevel) RunOption {
	return func(c *runConfig) {
		c.compressionEnabled = true
		if len(level) > 0 {
			c.compressionLevel = level[0]
		} else {
			c.compressionLevel = CompressionBestSpeed
		}
	}
}

// WithTrustedProxies enables Fiber's proxy-header trust path —
// `c.IP()` returns the rightmost address from X-Forwarded-For (or
// configured ProxyHeader) only if the immediate hop's IP is in the
// supplied CIDR allowlist. Without this, c.IP() returns the TCP
// peer address (typically the load balancer's IP) — bad for IP-based
// rate limiting / audit logs.
//
// Example:
//
//	fibermap.WithTrustedProxies("10.0.0.0/8", "192.168.0.0/16")
//
// Pass at least the cluster's pod CIDR and any load balancer's
// egress range.
func WithTrustedProxies(cidrs ...string) RunOption {
	return func(c *runConfig) { c.trustedProxies = cidrs }
}

// WithReqLogSlowThresholdOption raises the RequestLogger's level
// based on per-request latency:
//
//   - latency >= threshold → Warn
//   - latency < threshold  → Debug
//   - status >= 500        → Error (always)
//
// Default 0 = no threshold (every non-5xx request logged at Info —
// legacy behaviour).
//
// Plays nicely with WithRequestLogger — pass both: WithRequestLogger
// supplies the logger + skipPaths, this option adds the level split.
func WithReqLogSlowThresholdOption(d time.Duration) RunOption {
	return func(c *runConfig) { c.reqLogSlowThresh = d }
}

// WithNotFoundHandler installs a catch-all handler for unmatched
// routes (404). Default = Fiber's plain `404 Not Found`. The kit
// ships [NotFoundJSON] as a JSON-shape default.
//
//	eng.Run(fibermap.WithNotFoundHandler(fibermap.NotFoundJSON()))
func WithNotFoundHandler(h fiber.Handler) RunOption {
	return func(c *runConfig) { c.notFoundHandler = h }
}

// Run is the one-shot launcher. It creates (or uses) a fiber.App,
// installs Fiber-level middlewares, loads the YAML route tree
// (default "routes.yaml" on disk), mounts the engine, and blocks
// on Listen. On SIGINT/SIGTERM it triggers a graceful shutdown.
//
// Returns nil on graceful shutdown, the first non-nil error
// from load / mount / listen otherwise.
//
// The engine must have a ContextBuilder set and all referenced
// handlers / middleware / factories registered before Run — Run
// just calls Mount, which validates them.
//
// Defaults — see RunOption documentation.
func (e *Engine[T]) Run(opts ...RunOption) error {
	cfg := runConfig{
		routesPath:      "routes.yaml",
		shutdownTimeout: 10 * time.Second,
	}
	// Engine-wide defaults first (set by fibermap.Default[T]); explicit
	// options later so callers can always override.
	for _, opt := range e.defaultRunOpts {
		opt(&cfg)
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.addr == "" {
		if p := os.Getenv("PORT"); p != "" {
			cfg.addr = ":" + p
		} else {
			cfg.addr = ":3000"
		}
	}

	// Built-in defaults — applied AFTER user options so explicit
	// With*/Without* calls always win. Each `no*` flag tells us to
	// skip the corresponding default.
	if !cfg.noRecover && !cfg.withRecover {
		cfg.withRecover = true
		cfg.recoverLog = slog.Default()
	}
	if !cfg.noReqLog && !cfg.withReqLog {
		cfg.withReqLog = true
		cfg.reqLog = slog.Default()
		cfg.reqLogSkipPaths = []string{"/healthz", "/metrics"}
	}
	if !cfg.noHealthCheck && !cfg.healthCheckSet {
		cfg.healthCheckPath = "/healthz"
	}
	// RequestID is installed by prepending to cfg.uses. Default ON
	// unless WithoutRequestID was called.
	if !cfg.noRequestID {
		cfg.uses = append([]fiber.Handler{RequestID()}, cfg.uses...)
	}

	// Materialise the Fiber config by merging caller-supplied fields
	// with kit-driven overrides (BodyLimit, TrustedProxies). Caller's
	// explicit value wins on conflict.
	var fiberCfg fiber.Config
	if cfg.fiberConfig != nil {
		fiberCfg = *cfg.fiberConfig
	}
	if cfg.bodyLimit > 0 && fiberCfg.BodyLimit == 0 {
		fiberCfg.BodyLimit = cfg.bodyLimit
	}
	if len(cfg.trustedProxies) > 0 {
		fiberCfg.EnableTrustedProxyCheck = true
		if len(fiberCfg.TrustedProxies) == 0 {
			fiberCfg.TrustedProxies = cfg.trustedProxies
		} else {
			fiberCfg.TrustedProxies = append(fiberCfg.TrustedProxies, cfg.trustedProxies...)
		}
		if fiberCfg.ProxyHeader == "" {
			fiberCfg.ProxyHeader = fiber.HeaderXForwardedFor
		}
	}
	app := fiber.New(fiberCfg)

	// Health check registered FIRST so it bypasses every middleware
	// (auth, ContextBuilder, etc). The route handler doesn't call
	// c.Next(), so the Use chain never fires for /healthz.
	if cfg.healthCheckPath != "" {
		app.Get(cfg.healthCheckPath, func(c *fiber.Ctx) error {
			return c.SendString("ok")
		})
	}
	// Readiness alongside healthcheck for the same isolation
	// guarantee — K8s probes must not race the Use chain or
	// Recover middleware.
	if cfg.readinessPath != "" {
		app.Get(cfg.readinessPath, Readiness(cfg.readinessCheckers, cfg.readinessOpts...))
	}
	if cfg.withRecover {
		app.Use(Recover(cfg.recoverLog))
	}
	// CORS goes BEFORE most other middlewares so preflight (OPTIONS)
	// short-circuits without engaging rate limit / auth / logger.
	if cfg.corsCfg != nil {
		app.Use(CORS(*cfg.corsCfg))
	}
	if cfg.compressionEnabled {
		app.Use(Compression(cfg.compressionLevel))
	}
	if cfg.withReqLog {
		opts := []RequestLoggerOption{WithReqLogSkipPaths(cfg.reqLogSkipPaths...)}
		if cfg.reqLogSlowThresh > 0 {
			opts = append(opts, WithReqLogSlowThreshold(cfg.reqLogSlowThresh))
		}
		app.Use(RequestLoggerWithOptions(cfg.reqLog, opts...))
	}
	if cfg.rateLimitRPS > 0 {
		skip := cfg.rateLimitSkip
		if skip == nil {
			skip = []string{"/healthz", "/readyz", "/metrics"}
		}
		app.Use(rateLimitByIP(cfg.rateLimitRPS, cfg.rateLimitBurst, skip))
	}
	if cfg.metricsPath != "" {
		if cfg.metricsReg != nil && cfg.metricsGatherer != nil {
			app.Use(MetricsOn(cfg.metricsReg))
			app.Get(cfg.metricsPath, MetricsHandlerFor(cfg.metricsGatherer))
		} else {
			mw, reg := Metrics()
			app.Use(mw)
			app.Get(cfg.metricsPath, MetricsHandler(reg))
		}
	}
	for _, h := range cfg.uses {
		app.Use(h)
	}
	if cfg.configureApp != nil {
		cfg.configureApp(app)
	}

	// Auto-load only if the user hasn't already loaded a YAML
	// document. This lets callers preload from a different source
	// (LoadBytes, multiple LoadFS calls, etc.) and still use Run.
	if e.cfg == nil {
		if cfg.routesFS != nil {
			if err := e.LoadFS(cfg.routesFS, cfg.routesPath); err != nil {
				return err
			}
		} else {
			if err := e.LoadFile(cfg.routesPath); err != nil {
				return err
			}
		}
	}

	if err := e.Mount(app); err != nil {
		return err
	}

	// Catch-all 404 handler — registered AFTER Mount so it covers
	// every path that no engine route claimed.
	if cfg.notFoundHandler != nil {
		app.Use(cfg.notFoundHandler)
	}

	if cfg.disableSignals || cfg.shutdownTimeout <= 0 {
		err := app.Listen(cfg.addr)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	listenErr := make(chan error, 1)
	go func() {
		listenErr <- app.Listen(cfg.addr)
	}()

	select {
	case err := <-listenErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.shutdownTimeout)
		defer cancel()
		if err := app.ShutdownWithContext(shutdownCtx); err != nil {
			return err
		}
		// Drain whatever Listen returns post-shutdown so we don't leak
		// the goroutine. http.ErrServerClosed is the normal path.
		if err := <-listenErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}
