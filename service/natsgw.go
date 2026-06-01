package service

import (
	"github.com/gofiber/fiber/v2"

	"github.com/theizzatbek/gokit/clients/natsmap/natsgw"
	"github.com/theizzatbek/gokit/fibermap"
)

// WithNATSMapGateway mounts the kit's HTTP→NATS gateway under base
// (path "/publish" by default if base is "") with a `:subject` path
// param. Inbound request body is forwarded verbatim to
// natsmap.PublishRaw on the path-derived subject.
//
//	svc, _ := service.New[App, C](ctx, cfg,
//	    service.WithNATSMap(),
//	    service.WithNATSMapGateway("/publish",
//	        natsgw.WithSubjectAllowlist(
//	            "urlshort.link.created",
//	            "urlshort.link.visited",
//	        ),
//	        natsgw.WithHeaderForwarder("X-Tenant"),
//	    ),
//	)
//
// Requires [WithNATSMap] (the handler binds to svc.NATSMap). Without
// it, mounting silently no-ops and a Warn is logged at boot.
//
// Auth + role-gating belong in front of the mounted path — wire your
// Bearer + RequireRole middleware via [WithFiberMiddleware] OR
// publish behind a sidecar that mTLS-pins the caller. The gateway
// ships without auth on purpose; deployment topology dictates what
// fits.
func WithNATSMapGateway(base string, opts ...natsgw.Option) Option {
	return func(o *options) {
		o.natsgwEnable = true
		o.natsgwPath = base
		o.natsgwOpts = append(o.natsgwOpts, opts...)
	}
}

// mountNATSMapGateway wires the gateway route via WithConfigureApp.
// Called from runOptions. Silently skips when WithNATSMap wasn't
// also passed (svc.NATSMap nil).
func (s *Service[T, C]) mountNATSMapGateway() fibermap.RunOption {
	base := s.opts.natsgwPath
	if base == "" {
		base = "/publish"
	}
	path := base + "/:subject"
	opts := append([]natsgw.Option(nil), s.opts.natsgwOpts...)
	return fibermap.WithConfigureApp(func(app *fiber.App) {
		if s.NATSMap == nil {
			if s.logger != nil {
				s.logger.Warn("service: WithNATSMapGateway ignored — NATSMap not wired",
					"path", path)
			}
			return
		}
		app.Post(path, natsgw.Handler(s.NATSMap, opts...))
	})
}
