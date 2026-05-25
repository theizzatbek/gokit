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

	withReqLog      bool
	reqLog          *slog.Logger
	reqLogSkipPaths []string
	noReqLog        bool

	noRequestID bool

	metricsPath string
	metricsSet  bool
	noMetrics   bool
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

	var app *fiber.App
	if cfg.fiberConfig != nil {
		app = fiber.New(*cfg.fiberConfig)
	} else {
		app = fiber.New()
	}

	// Health check registered FIRST so it bypasses every middleware
	// (auth, ContextBuilder, etc). The route handler doesn't call
	// c.Next(), so the Use chain never fires for /healthz.
	if cfg.healthCheckPath != "" {
		app.Get(cfg.healthCheckPath, func(c *fiber.Ctx) error {
			return c.SendString("ok")
		})
	}
	if cfg.withRecover {
		app.Use(Recover(cfg.recoverLog))
	}
	if cfg.withReqLog {
		app.Use(RequestLogger(cfg.reqLog, cfg.reqLogSkipPaths...))
	}
	if cfg.metricsPath != "" {
		mw, reg := Metrics()
		app.Use(mw)
		app.Get(cfg.metricsPath, MetricsHandler(reg))
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
