package service

import (
	"os"

	"github.com/gofiber/fiber/v2"

	"github.com/theizzatbek/gokit/auth"
	xerrs "github.com/theizzatbek/gokit/errs"
	"github.com/theizzatbek/gokit/fibermap"
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
func (s *Service[T, C]) Run() error {
	defer s.Close()
	if path := resolvePath(s.cfg.Routes.Path, DefaultRoutesPath, s.cfg.Routes.Enabled); path != "" {
		if _, err := os.Stat(path); err != nil {
			return xerrs.Wrapf(err, xerrs.KindNotFound, CodeRoutesYAMLNotFound,
				"service: routes yaml not found at %q (set ROUTES_PATH or disable with ROUTES_ENABLED=false)", path)
		}
		if err := s.Engine.LoadFile(path); err != nil {
			return err
		}
	}
	return s.Engine.Run(s.runOptions()...)
}

func (s *Service[T, C]) runOptions() []fibermap.RunOption {
	out := []fibermap.RunOption{
		fibermap.WithAddr(s.cfg.Service.Addr),
		fibermap.WithRequestLogger(s.logger),
		fibermap.WithMetrics("/metrics"),
		fibermap.WithHealthCheck("/healthz"),
		fibermap.WithRecover(s.logger),
	}
	var fiberMW []fiber.Handler
	if s.Auth != nil && !s.opts.skipBearerLayer {
		fiberMW = append(fiberMW, s.Auth.Bearer(auth.BearerOptional))
	}
	fiberMW = append(fiberMW, s.opts.fiberMiddleware...)
	if len(fiberMW) > 0 {
		out = append(out, fibermap.WithUse(fiberMW...))
	}
	out = append(out, s.opts.runOpts...)
	return out
}

// Close releases owned long-lived resources in reverse construction
// order. Idempotent + safe to call from defer alongside Run.
func (s *Service[T, C]) Close() {
	if s == nil || s.closed {
		return
	}
	s.closed = true
	if s.NATSMap != nil {
		_ = s.NATSMap.Drain()
	}
	if s.NATS != nil {
		s.NATS.Close()
	}
	if s.DB != nil {
		s.DB.Close()
	}
}
