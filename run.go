package fibermap

import (
	"context"
	"errors"
	"io/fs"
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
//   - Listen on ":3000".
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
}

// WithAddr overrides the listen address. Default ":3000".
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
		addr:            ":3000",
		routesPath:      "routes.yaml",
		shutdownTimeout: 10 * time.Second,
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	var app *fiber.App
	if cfg.fiberConfig != nil {
		app = fiber.New(*cfg.fiberConfig)
	} else {
		app = fiber.New()
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
