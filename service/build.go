package service

import (
	"context"
	"fmt"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/theizzatbek/gokit/auth"
	"github.com/theizzatbek/gokit/auth/fibermount"
	"github.com/theizzatbek/gokit/auth/refreshpg"
	"github.com/theizzatbek/gokit/clients/apimap"
	"github.com/theizzatbek/gokit/clients/httpc"
	natsclient "github.com/theizzatbek/gokit/clients/nats"
	"github.com/theizzatbek/gokit/db"
	xerrs "github.com/theizzatbek/gokit/errs"
	"github.com/theizzatbek/gokit/fibermap"
	"github.com/theizzatbek/gokit/fibermap/openapi"
)

// New constructs the bundled Service. Subsystems are built in dependency
// order; failures wrap subpkg errors with a service_* Code so callers can
// distinguish "DB connect failed" from "apimap load failed" without
// inspecting nested errors.
func New[T any, C any](ctx context.Context, cfg Config, opts ...Option) (*Service[T, C], error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	o := &options{}
	for _, fn := range opts {
		fn(o)
	}

	logger := o.logger
	if logger == nil {
		logger = newLogger(cfg.Service.LogFormat, cfg.Service.LogLevel)
	}
	metrics := o.metrics
	if metrics == nil {
		metrics = prometheus.NewRegistry()
	}

	s := &Service[T, C]{
		cfg:     cfg,
		logger:  logger,
		metrics: metrics,
		opts:    o,
	}

	if err := s.buildDB(ctx); err != nil {
		return nil, err
	}
	if err := s.buildAuth(); err != nil {
		s.Close()
		return nil, err
	}
	if err := s.buildHTTPC(); err != nil {
		s.Close()
		return nil, err
	}
	if err := s.buildAPIMap(); err != nil {
		s.Close()
		return nil, err
	}
	if err := s.buildNATS(ctx); err != nil {
		s.Close()
		return nil, err
	}
	if err := s.buildEngine(); err != nil {
		s.Close()
		return nil, err
	}
	if err := s.mountAuthHandlers(); err != nil {
		s.Close()
		return nil, err
	}
	if err := s.mountOpenAPI(); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

func (s *Service[T, C]) buildDB(ctx context.Context) error {
	if s.cfg.DB.User == "" {
		return nil
	}
	d, err := db.Connect(ctx, s.cfg.DB, db.WithLogger(s.logger))
	if err != nil {
		return xerrs.Wrap(err, xerrs.KindUnavailable, CodeDBConnectFailed, "service: db connect failed")
	}
	s.DB = d
	return nil
}

func (s *Service[T, C]) buildAuth() error {
	if s.cfg.Auth.PrivateKeyPEM == "" {
		return nil
	}
	keySet, err := auth.LoadKeysFromPEM(s.cfg.Auth.KID, map[string][]byte{
		s.cfg.Auth.KID: []byte(s.cfg.Auth.PrivateKeyPEM),
	})
	if err != nil {
		return xerrs.Wrap(err, xerrs.KindValidation, CodeAuthInvalidKey, "service: auth key invalid")
	}
	a, err := auth.New[C](auth.Config{
		Issuer:     s.cfg.Auth.Issuer,
		Keys:       keySet,
		AccessTTL:  s.cfg.Auth.AccessTTL,
		RefreshTTL: s.cfg.Auth.RefreshTTL,
	}, auth.WithRefreshStore(refreshpg.New(s.DB)), auth.WithLogger(s.logger))
	if err != nil {
		return xerrs.Wrap(err, xerrs.KindInternal, CodeAuthInvalidKey, "service: auth.New failed")
	}
	s.Auth = a
	s.Hasher = auth.NewHasher(auth.DefaultParams())
	return nil
}

func (s *Service[T, C]) buildHTTPC() error {
	httpcOpts := append([]httpc.Option{httpc.WithLogger(s.logger), httpc.WithMetrics(s.metrics)}, s.opts.httpcOpts...)
	c, err := httpc.New(s.cfg.HTTPC, httpcOpts...)
	if err != nil {
		return xerrs.Wrap(err, xerrs.KindValidation, CodeHTTPCNewFailed, "service: httpc.New failed")
	}
	s.HTTPC = c
	return nil
}

func (s *Service[T, C]) buildAPIMap() error {
	if s.cfg.APIMap.Path == "" {
		return nil
	}
	eng := apimap.New()
	if err := eng.LoadFile(s.cfg.APIMap.Path); err != nil {
		return xerrs.Wrap(err, xerrs.KindValidation, CodeAPIMapLoadFailed,
			fmt.Sprintf("service: apimap load %q failed", s.cfg.APIMap.Path))
	}
	if s.opts.apimapRegistration != nil {
		s.opts.apimapRegistration(eng)
	}
	apimapOpts := append([]apimap.Option{apimap.WithLogger(s.logger), apimap.WithMetrics(s.metrics)}, s.opts.apimapOpts...)
	c, err := eng.Build(apimapOpts...)
	if err != nil {
		return xerrs.Wrap(err, xerrs.KindValidation, CodeAPIMapLoadFailed, "service: apimap build failed")
	}
	s.APIMap = c
	return nil
}

func (s *Service[T, C]) buildNATS(ctx context.Context) error {
	if s.cfg.NATS.URL == "" {
		return nil
	}
	natsOpts := append([]natsclient.Option{natsclient.WithLogger(s.logger), natsclient.WithMetrics(s.metrics)}, s.opts.natsOpts...)
	c, err := natsclient.Connect(ctx, natsclient.Config{URL: s.cfg.NATS.URL, Name: s.cfg.NATS.Name}, natsOpts...)
	if err != nil {
		return xerrs.Wrap(err, xerrs.KindUnavailable, CodeNATSConnectFailed, "service: nats connect failed")
	}
	s.NATS = c
	return nil
}

func (s *Service[T, C]) buildEngine() error {
	s.Engine = fibermap.Default[T]()
	s.Engine.SetValidator(validator.New(validator.WithRequiredStructEnabled()))
	return nil
}

func (s *Service[T, C]) mountAuthHandlers() error {
	if s.Auth == nil || s.opts.skipAuthHandlers {
		return nil
	}
	if err := fibermount.MountMiddlewareFactories(s.Engine, s.Auth); err != nil {
		return xerrs.Wrap(err, xerrs.KindInternal, CodeAuthInvalidKey, "service: fibermount.MountMiddlewareFactories failed")
	}
	wrap := func(h fiber.Handler) fibermap.HandlerFunc[T] {
		return func(c *fibermap.Context[T]) error { return h(c.Ctx) }
	}
	s.Engine.Add("POST", "/auth/login", "auth.login", wrap(s.Auth.LoginHandler))
	s.Engine.Add("POST", "/auth/refresh", "auth.refresh", wrap(s.Auth.RefreshHandler))
	s.Engine.Add("POST", "/auth/logout", "auth.logout", wrap(s.Auth.LogoutHandler))
	return nil
}

func (s *Service[T, C]) mountOpenAPI() error {
	if s.opts.openapiInfo == nil {
		return nil
	}
	openapiOpts := append([]openapi.Option{openapi.WithInfo(*s.opts.openapiInfo)}, s.opts.openapiOpts...)
	gen := openapi.NewGenerator(s.Engine, openapiOpts...)
	if err := gen.Mount(); err != nil {
		return xerrs.Wrap(err, xerrs.KindInternal, CodeOpenAPIMountFailed, "service: openapi mount failed")
	}
	return nil
}
