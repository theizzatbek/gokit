package svckit

import (
	"os"

	"github.com/gofiber/fiber/v2"

	"github.com/theizzatbek/gokit/auth"
	xerrs "github.com/theizzatbek/gokit/errs"
	"github.com/theizzatbek/gokit/fibermap"
	"github.com/theizzatbek/gokit/fibermap/openapi"
)

// Run applies the production-ops bundle on top of any user-supplied
// RunOptions and blocks on engine.Run. SIGINT/SIGTERM handling is
// handled by Engine.Run itself. Close is deferred internally — calling
// it again from main is safe (Close is idempotent).
//
// When Config.Routes.Enabled is true (or Config.Routes.Path is set),
// Run loads the routes YAML via Engine.LoadFile after user-side
// RegisterHandler calls and before Engine.Mount. Missing file returns
// *errs.Error{Code: CodeRoutesYAMLNotFound}.
//
// OpenAPI: if WithOpenAPI() was passed OR routes.yaml contains a
// top-level `openapi:` block, Run generates and mounts the document
// (/openapi.json + /docs) before Engine.Run.
func (s *Service[T, C]) Run() error {
	defer s.Close()
	if s.opts.routesEnable {
		s.cfg.Routes.Enabled = true
	}
	routesPath := resolvePathInDir(s.cfg.Service.ConfigsDir, s.cfg.Routes.Path, DefaultRoutesPath, s.cfg.Routes.Enabled)
	if routesPath != "" {
		if _, err := os.Stat(routesPath); err != nil {
			return xerrs.Wrapf(err, xerrs.KindNotFound, CodeRoutesYAMLNotFound,
				"svckit: routes yaml not found at %q (set ROUTES_PATH or disable with ROUTES_ENABLED=false)", routesPath)
		}
		if err := s.Engine.LoadFile(routesPath); err != nil {
			return err
		}
	}
	if err := s.mountOpenAPI(routesPath); err != nil {
		return err
	}
	return s.Engine.Run(s.runOptions()...)
}

// mountOpenAPI generates and mounts the OpenAPI document if either:
//   - WithOpenAPI() was passed, OR
//   - routes.yaml contains a top-level openapi: block.
//
// YAML opts apply first, then user opts (Info: last-write-wins;
// Servers / SecuritySchemes / MiddlewareSecurity: append).
func (s *Service[T, C]) mountOpenAPI(routesPath string) error {
	var yamlOpts []openapi.Option
	if routesPath != "" {
		y, err := parseOpenAPIBlock(routesPath)
		if err != nil {
			return err
		}
		if y != nil {
			yamlOpts = y.toOpenAPIOptions()
		}
	}
	if !s.opts.openapiEnable && len(yamlOpts) == 0 {
		return nil
	}
	allOpts := append(yamlOpts, s.opts.openapiOpts...)
	gen := openapi.NewGenerator(s.Engine, allOpts...)
	if err := gen.Mount(); err != nil {
		return xerrs.Wrap(err, xerrs.KindInternal, CodeOpenAPIMountFailed,
			"svckit: openapi mount failed")
	}
	return nil
}

// runOptions assembles the production-ops fibermap.RunOption bundle:
// addr, request logging, metrics, health/readiness/preflight
// endpoints, panic recovery, the app-level fiber config and
// middleware chain, TLS, and whatever run options mods contributed
// via Host.AddRunOption or the caller via [WithRunOptions] — both
// accumulate into s.opts.runOpts.
func (s *Service[T, C]) runOptions() []fibermap.RunOption {
	out := []fibermap.RunOption{
		fibermap.WithAddr(s.cfg.Service.Addr),
		fibermap.WithRequestLogger(s.logger),
		fibermap.WithMetrics("/metrics"),
		fibermap.WithHealthCheck("/healthz"),
		fibermap.WithRecover(s.logger),
	}
	if !s.opts.skipReadiness {
		path := s.opts.readinessPath
		if path == "" {
			path = "/readyz"
		}
		checkers := s.readinessCheckers()
		out = append(out, fibermap.WithReadiness(path, checkers...))
		if s.opts.readinessTimeout > 0 {
			out = append(out, fibermap.WithReadinessOpts(
				fibermap.WithReadinessTimeout(s.opts.readinessTimeout)))
		}
	}
	s.mountDevTools()
	if s.opts.preflightEnable {
		path := s.opts.preflightPath
		if path == "" {
			path = "/preflight"
		}
		handler := s.preflightHandler()
		out = append(out, fibermap.WithConfigureApp(func(app *fiber.App) {
			app.Get(path, handler)
		}))
	}
	out = append(out, fibermap.WithFiberConfig(s.buildFiberConfig()))
	// Route /metrics through the unified service registry when the
	// configured Registerer is also a Gatherer (the default
	// prometheus.NewRegistry() satisfies both). Otherwise leave the
	// fibermap-private registry in place and only fibermap_http_*
	// series get exposed — the caller is expected to mount their own
	// scrape endpoint over their custom Registerer in that case.
	if reg := s.metricsRegistry(); reg != nil {
		out = append(out, fibermap.WithMetricsRegistry(reg))
	}
	if fiberMW := s.appLevelMiddleware(); len(fiberMW) > 0 {
		out = append(out, fibermap.WithUse(fiberMW...))
	}
	if cert, key := s.resolveTLS(); cert != "" || key != "" {
		// Forwarded even when incomplete — fibermap.Run rejects a
		// half-pair with invalid_tls_config rather than silently
		// starting plain HTTP.
		out = append(out, fibermap.WithTLS(cert, key))
	}
	// Anything a mod queued via Host.AddRunOption (e.g. an
	// HTTP→NATS gateway route) or the caller via WithRunOptions.
	// Both accumulate into the same slice — see WithRunOptions.
	out = append(out, s.opts.runOpts...)
	return out
}

// resolveTLS picks the TLS cert/key pair Run forwards to
// fibermap.WithTLS. Caller-supplied [WithTLS] wins; otherwise the
// env-driven ServiceConfig pair (TLS_CERT_FILE / TLS_KEY_FILE)
// applies. Empty pair → plain HTTP.
func (s *Service[T, C]) resolveTLS() (cert, key string) {
	if s.opts.tlsCert != "" || s.opts.tlsKey != "" {
		return s.opts.tlsCert, s.opts.tlsKey
	}
	return s.cfg.Service.TLSCertFile, s.cfg.Service.TLSKeyFile
}

// appLevelMiddleware assembles the fiber.App-level (WithUse) chain that
// Run installs before the engine's contextInit. Order:
//
//		CORS → SecurityHeaders → Bearer(BearerOptional) →
//		authSubjectBridge → LoggerInjector → opts.fiberMiddleware
//
//	  - CORS runs BEFORE the bearer layer so a present-but-invalid
//	    token's 401 still carries Access-Control-Allow-Origin (the
//	    browser blocks header-less cross-origin 401s and the front-end
//	    "caught 401 → refresh" flow never fires). Preflight OPTIONS
//	    short-circuits here without engaging auth.
//	  - The bearer layer stays BEFORE the engine's contextInit (which
//	    runs after this whole chain) — ContextBuilder reads the
//	    principal from Locals that Bearer fills.
//	  - opts.fiberMiddleware is last — this is also where mods land
//	    their own fiber.Handler via Host.UseFiber (e.g. an otel span
//	    handler), so a mod that needs to run earlier in the chain must
//	    say so explicitly; chain position is not otherwise negotiable.
func (s *Service[T, C]) appLevelMiddleware() []fiber.Handler {
	var fiberMW []fiber.Handler
	if s.opts.corsHandler != nil {
		fiberMW = append(fiberMW, s.opts.corsHandler)
	}
	if !s.opts.skipSecurityHeaders {
		fiberMW = append(fiberMW, fibermap.SecurityHeaders(s.opts.securityHeaderOpts...))
	}
	if s.Auth != nil && !s.opts.skipBearerLayer {
		fiberMW = append(fiberMW, s.Auth.Bearer(auth.BearerOptional))
		// Pull the principal subject (set by Bearer above) into the
		// shared Locals slot LoggerFrom reads at call time. Bearer
		// stores the full Principal[C] under its private key; we
		// pluck the public Subject string into a separate slot so
		// the fibermap package doesn't need a runtime dependency on
		// auth's Principal type.
		fiberMW = append(fiberMW, s.authSubjectBridge())
	}
	if !s.opts.skipLoggerInjector {
		fiberMW = append(fiberMW, fibermap.LoggerInjector(s.logger))
	}
	fiberMW = append(fiberMW, s.opts.fiberMiddleware...)
	return fiberMW
}

// buildFiberConfig assembles the fiber.Config used by every
// Service.Run, regardless of caller-passed options. The ErrorHandler
// is wired unconditionally so callers returning *errs.Error from
// handlers always get typed `{code, message, details[]}` JSON even
// when [WithBodyLimit] is not set.
//
// ErrorHandler precedence: [WithErrorHandler] override wins when
// non-nil; otherwise the kit installs [fibermap.ErrorHandler] over
// s.logger.
//
// BodyLimit defaults to fiber's own default (4 MiB) — only overridden
// when [WithBodyLimit] supplied a positive value.
func (s *Service[T, C]) buildFiberConfig() fiber.Config {
	errHandler := s.opts.errorHandler
	if errHandler == nil {
		errHandler = fibermap.ErrorHandler(s.logger)
	}
	cfg := fiber.Config{ErrorHandler: errHandler}
	if s.opts.bodyLimit > 0 {
		cfg.BodyLimit = s.opts.bodyLimit
	}
	return cfg
}
