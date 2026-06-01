package service

import (
	"github.com/gofiber/fiber/v2"

	"github.com/theizzatbek/gokit/fibermap"
	"github.com/theizzatbek/gokit/fibermap/dev"
)

// WithDevMode wires the kit's dev-only DX tools:
//
//   - HTML error pages with stack traces (on `Accept: text/html`)
//   - /_dev/routes inspector — list every mounted route
//   - /_dev/config inspector — effective env vars with secret
//     redaction
//
// prefix is the URL prefix for the inspector endpoints. Pass "" for
// the default "/_dev".
//
// SAFETY: this option is a no-op when Config.Service.Env != "dev".
// Operators can pass it unconditionally; production deployments stay
// safe by virtue of their ENV != "dev". A warning is logged at
// service.New time if the option was passed in a non-dev env so the
// noop is visible.
func WithDevMode(prefix string, opts ...dev.ConfigOption) Option {
	return func(o *options) {
		o.devEnable = true
		o.devPrefix = prefix
		o.devConfigOpts = append(o.devConfigOpts, opts...)
	}
}

// mountDevTools registers the dev inspectors when ENV=dev. Wraps
// the fiber error handler with [dev.ErrorHandler] so HTML clients
// get rendered error pages.
func (s *Service[T, C]) mountDevTools() {
	if !s.opts.devEnable {
		return
	}
	if s.cfg.Service.Env != "dev" {
		if s.logger != nil {
			s.logger.Warn("service: WithDevMode requested but ENV != dev — dev inspectors not mounted",
				"env", s.cfg.Service.Env)
		}
		return
	}
	prefix := s.opts.devPrefix
	if prefix == "" {
		prefix = "/_dev"
	}
	// Routes + config inspectors via WithConfigureApp because they
	// need a direct *fiber.App reference.
	routesPath := prefix + "/routes"
	configPath := prefix + "/config"
	opts := append([]dev.ConfigOption(nil), s.opts.devConfigOpts...)
	s.opts.runOpts = append(s.opts.runOpts,
		fibermap.WithConfigureApp(func(app *fiber.App) {
			app.Get(routesPath, dev.RoutesHandler(app))
			app.Get(configPath, dev.ConfigHandler(opts...))
		}),
	)
}
