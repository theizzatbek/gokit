package service

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/theizzatbek/gokit/auth"
	"github.com/theizzatbek/gokit/auth/fibermount"
	"github.com/theizzatbek/gokit/auth/refreshpg"
	"github.com/theizzatbek/gokit/clients/apimap"
	"github.com/theizzatbek/gokit/clients/httpc"
	natsclient "github.com/theizzatbek/gokit/clients/nats"
	"github.com/theizzatbek/gokit/clients/natsmap"
	"github.com/theizzatbek/gokit/db"
	xerrs "github.com/theizzatbek/gokit/errs"
	"github.com/theizzatbek/gokit/fibermap"
)

// New constructs the bundled Service. Subsystems are built in dependency
// order; failures wrap subpkg errors with a service_* Code so callers can
// distinguish "DB connect failed" from "apimap load failed" without
// inspecting nested errors.
func New[T any, C any](ctx context.Context, cfg Config, opts ...Option) (*Service[T, C], error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	if cfg.Service.NodeName == "" {
		if h, err := os.Hostname(); err == nil {
			cfg.Service.NodeName = h
		}
	}

	o := &options{}
	for _, fn := range opts {
		fn(o)
	}

	logger := o.logger
	if logger == nil {
		logger = newLogger(cfg.Service.LogFormat, cfg.Service.LogLevel,
			cfg.Service.NodeName, cfg.Service.ServerGroup)
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

	// OTel must run BEFORE the build phase — it mutates
	// opts.fiberMiddleware (prepending otelfiber) and opts.httpcOpts
	// (adding the otelhttp transport wrap) which are consumed by
	// buildHTTPC and run.go.
	if err := s.setupOtel(ctx); err != nil {
		return nil, err
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
	if err := s.buildNATSMap(ctx); err != nil {
		s.Close()
		return nil, err
	}
	if err := s.buildEngine(); err != nil {
		s.Close()
		return nil, err
	}
	if err := s.mountAuthMiddleware(); err != nil {
		s.Close()
		return nil, err
	}
	s.registerOtelShutdown()
	s.startRefreshGC()
	return s, nil
}

func (s *Service[T, C]) buildDB(ctx context.Context) error {
	if s.cfg.DB.User == "" {
		return nil
	}
	applyDBAppNameDefault(&s.cfg.DB.AppName, s.cfg.Service.NodeName)
	applyConnectRetryDefaults(s.opts.skipConnectRetry,
		&s.cfg.DB.ConnectMaxRetries,
		&s.cfg.DB.ConnectBackoffBase,
		&s.cfg.DB.ConnectBackoffMax)
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
	store := refreshpg.New(s.DB)
	a, err := auth.New[C](auth.Config{
		Issuer:     s.cfg.Auth.Issuer,
		Keys:       keySet,
		AccessTTL:  s.cfg.Auth.AccessTTL,
		RefreshTTL: s.cfg.Auth.RefreshTTL,
	}, auth.WithRefreshStore(store), auth.WithLogger(s.logger))
	if err != nil {
		return xerrs.Wrap(err, xerrs.KindInternal, CodeAuthInvalidKey, "service: auth.New failed")
	}
	s.Auth = a
	s.refreshStore = store
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
	if s.opts.apimapEnable {
		s.cfg.APIMap.Enabled = true
	}
	path := resolvePath(s.cfg.APIMap.Path, DefaultAPIMapPath, s.cfg.APIMap.Enabled)
	if path == "" {
		return nil
	}
	if _, err := os.Stat(path); err != nil {
		return xerrs.Wrapf(err, xerrs.KindNotFound, CodeAPIMapYAMLNotFound,
			"service: apimap yaml not found at %q (set APIMAP_PATH or disable with APIMAP_ENABLED=false)", path)
	}
	var apimapNewOpts []apimap.EngineOption
	if s.opts.apimapEnv != nil {
		apimapNewOpts = append(apimapNewOpts, apimap.WithEnv(s.opts.apimapEnv))
	}
	eng := apimap.New(apimapNewOpts...)
	if err := eng.LoadFile(path); err != nil {
		return xerrs.Wrap(err, xerrs.KindValidation, CodeAPIMapLoadFailed,
			fmt.Sprintf("service: apimap load %q failed", path))
	}
	if s.opts.apimapRegistration != nil {
		s.opts.apimapRegistration(eng)
	}
	apimapOpts := append([]apimap.Option{apimap.WithLogger(s.logger)}, s.opts.apimapOpts...)
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
	applyConnectRetryDefaults(s.opts.skipConnectRetry,
		&s.cfg.NATS.ConnectMaxRetries,
		&s.cfg.NATS.ConnectBackoffBase,
		&s.cfg.NATS.ConnectBackoffMax)
	natsOpts := append([]natsclient.Option{natsclient.WithLogger(s.logger), natsclient.WithMetrics(s.metrics)}, s.opts.natsOpts...)
	natsName := s.cfg.NATS.Name
	if natsName == "" {
		natsName = s.cfg.Service.NodeName
	}
	c, err := natsclient.Connect(ctx, natsclient.Config{
		URL:                s.cfg.NATS.URL,
		Name:               natsName,
		ConnectMaxRetries:  s.cfg.NATS.ConnectMaxRetries,
		ConnectBackoffBase: s.cfg.NATS.ConnectBackoffBase,
		ConnectBackoffMax:  s.cfg.NATS.ConnectBackoffMax,
	}, natsOpts...)
	if err != nil {
		return xerrs.Wrap(err, xerrs.KindUnavailable, CodeNATSConnectFailed, "service: nats connect failed")
	}
	s.NATS = c
	return nil
}

func (s *Service[T, C]) buildNATSMap(ctx context.Context) error {
	if s.opts.natsmapEnable {
		s.cfg.NATSMap.Enabled = true
	}
	subs := resolvePath(s.cfg.NATSMap.SubscribersPath, DefaultNATSMapSubscribersPath, s.cfg.NATSMap.Enabled)
	pubs := resolvePath(s.cfg.NATSMap.PublishersPath, DefaultNATSMapPublishersPath, s.cfg.NATSMap.Enabled)
	if subs == "" && pubs == "" {
		return nil
	}
	if s.NATS == nil {
		return xerrs.Validation(CodeNATSMapNeedsNATS,
			"service: NATSMap requires NATS (subscribers + publishers need a connection)")
	}
	// Override paths (user explicitly set SubscribersPath/PublishersPath)
	// are strict: missing file → error. Default paths (used because
	// Enabled=true and no override) are silent-skip on miss; this
	// supports publish-only and subscribe-only services that only
	// drop one of the two default files.
	var firstErr error
	check := func(resolved string, isOverride bool) string {
		if resolved == "" {
			return ""
		}
		if _, err := os.Stat(resolved); err != nil {
			if isOverride && firstErr == nil {
				firstErr = xerrs.Wrapf(err, xerrs.KindNotFound, CodeNATSMapYAMLNotFound,
					"service: natsmap yaml not found at %q", resolved)
			}
			return ""
		}
		return resolved
	}
	subs = check(subs, s.cfg.NATSMap.SubscribersPath != "")
	pubs = check(pubs, s.cfg.NATSMap.PublishersPath != "")
	if firstErr != nil {
		return firstErr
	}
	if subs == "" && pubs == "" {
		return xerrs.NotFoundf(CodeNATSMapYAMLNotFound,
			"service: natsmap enabled but neither %q nor %q found in CWD",
			DefaultNATSMapSubscribersPath, DefaultNATSMapPublishersPath)
	}

	var natsmapNewOpts []natsmap.EngineOption
	if s.opts.natsmapEnv != nil {
		natsmapNewOpts = append(natsmapNewOpts, natsmap.WithEnv(s.opts.natsmapEnv))
	}
	if s.cfg.Service.ServerGroup != "" {
		natsmapNewOpts = append(natsmapNewOpts, natsmap.WithServerGroup(s.cfg.Service.ServerGroup))
	}
	eng := natsmap.New(natsmapNewOpts...)
	if subs != "" {
		if err := eng.LoadFile(subs); err != nil {
			return xerrs.Wrap(err, xerrs.KindValidation, CodeNATSMapLoadFailed,
				fmt.Sprintf("service: natsmap load subscribers %q failed", subs))
		}
	}
	if pubs != "" {
		if err := eng.LoadFile(pubs); err != nil {
			return xerrs.Wrap(err, xerrs.KindValidation, CodeNATSMapLoadFailed,
				fmt.Sprintf("service: natsmap load publishers %q failed", pubs))
		}
	}
	if s.opts.natsmapRegistration != nil {
		s.opts.natsmapRegistration(eng)
	}
	rt, err := eng.Build(ctx, s.NATS, natsmap.WithLogger(s.logger), natsmap.WithMetrics(s.metrics))
	if err != nil {
		return xerrs.Wrap(err, xerrs.KindValidation, CodeNATSMapLoadFailed,
			"service: natsmap build failed")
	}
	s.NATSMap = rt
	return nil
}

func (s *Service[T, C]) buildEngine() error {
	s.Engine = fibermap.Default[T]()
	if s.opts.validator != nil {
		s.Engine.SetValidator(s.opts.validator)
	} else {
		s.Engine.SetValidator(validator.New(validator.WithRequiredStructEnabled()))
	}
	return nil
}

// mountAuthMiddleware wires Auth's bearer-style middleware factories onto the
// engine so routes.yaml entries like `middleware: - bearer: []` resolve. The
// service no longer mounts /auth/login, /refresh, /logout — those endpoints
// are the caller's responsibility (typically a fibermap handler that parses
// the chosen credential format and calls Auth.IssueLogin / IssueRefresh /
// Logout). See examples/urlshort/internal/users for a password-login example.
func (s *Service[T, C]) mountAuthMiddleware() error {
	if s.Auth == nil {
		return nil
	}
	if err := fibermount.MountMiddlewareFactories(s.Engine, s.Auth); err != nil {
		return xerrs.Wrap(err, xerrs.KindInternal, CodeAuthInvalidKey, "service: fibermount.MountMiddlewareFactories failed")
	}
	return nil
}

// applyConnectRetryDefaults centralises the rule:
//   - skip=true → no injection (cfg stays as user wrote it).
//   - maxRetries == -1 → user explicit "no retry"; normalize to 0.
//   - maxRetries == 0 → inject 5 (K8s-friendly default).
//   - base == 0 → inject 1s.
//   - max == 0 → inject 16s.
//
// Pure function — easy to unit test without constructing a Service.
func applyConnectRetryDefaults(skip bool, maxRetries *int, base, max *time.Duration) {
	if skip {
		return
	}
	if *maxRetries == -1 {
		*maxRetries = 0
		return
	}
	if *maxRetries == 0 {
		*maxRetries = 5
	}
	if *base == 0 {
		*base = time.Second
	}
	if *max == 0 {
		*max = 16 * time.Second
	}
}

// applyDBAppNameDefault copies nodeName into appName when appName is empty.
// Used by buildDB so each instance is visible in pg_stat_activity as the
// pod hostname (or whatever the user set in Service.NodeName).
func applyDBAppNameDefault(appName *string, nodeName string) {
	if *appName == "" && nodeName != "" {
		*appName = nodeName
	}
}
