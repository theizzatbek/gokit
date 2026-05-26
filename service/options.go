package service

import (
	"log/slog"

	"github.com/gofiber/fiber/v2"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/theizzatbek/gokit/clients/apimap"
	"github.com/theizzatbek/gokit/clients/httpc"
	natsclient "github.com/theizzatbek/gokit/clients/nats"
	"github.com/theizzatbek/gokit/clients/natsmap"
	"github.com/theizzatbek/gokit/fibermap"
	"github.com/theizzatbek/gokit/fibermap/openapi"
)

// Option configures service.New beyond what Config covers.
type Option func(*options)

type options struct {
	logger              *slog.Logger
	metrics             prometheus.Registerer
	openapiInfo         *openapi.Info // nil = disabled
	openapiOpts         []openapi.Option
	fiberMiddleware     []fiber.Handler
	skipAuthHandlers    bool
	skipBearerLayer     bool
	httpcOpts           []httpc.Option
	apimapOpts          []apimap.Option
	apimapRegistration  func(*apimap.Engine)
	apimapEnv           map[string]string
	apimapEnable        bool
	natsmapRegistration func(*natsmap.Engine)
	natsmapEnv          map[string]string
	natsmapEnable       bool
	natsOpts            []natsclient.Option
	routesEnable        bool
	runOpts             []fibermap.RunOption
}

// WithLogger overrides the auto-built slog.Logger.
func WithLogger(l *slog.Logger) Option { return func(o *options) { o.logger = l } }

// WithMetrics overrides the default prometheus.NewRegistry().
func WithMetrics(reg prometheus.Registerer) Option {
	return func(o *options) { o.metrics = reg }
}

// WithOpenAPI enables OpenAPI generation — /openapi.json + /docs are
// mounted by service.New. Pass extra openapi.Option entries (security
// schemes, default responses, servers) after info.
func WithOpenAPI(info openapi.Info, opts ...openapi.Option) Option {
	return func(o *options) {
		o.openapiInfo = &info
		o.openapiOpts = opts
	}
}

// WithFiberMiddleware appends fiber-level middleware installed BEFORE
// the engine's contextInit. The auto-installed Bearer-optional layer
// stays unless WithoutBearerOptionalLayer is also passed.
func WithFiberMiddleware(handlers ...fiber.Handler) Option {
	return func(o *options) { o.fiberMiddleware = append(o.fiberMiddleware, handlers...) }
}

// WithoutAuthHandlers skips the auto-mount of /auth/login, /refresh,
// /logout. Use when you want full manual control over the auth route
// surface.
func WithoutAuthHandlers() Option {
	return func(o *options) { o.skipAuthHandlers = true }
}

// WithoutBearerOptionalLayer skips installing auth.Bearer(BearerOptional)
// at the fiber.App level. Only sensible if you have no auth or want to
// orchestrate the layer yourself.
func WithoutBearerOptionalLayer() Option {
	return func(o *options) { o.skipBearerLayer = true }
}

// WithHTTPCOptions appends to the httpc options applied by service.New
// (logger + metrics are already auto-applied).
func WithHTTPCOptions(opts ...httpc.Option) Option {
	return func(o *options) { o.httpcOpts = append(o.httpcOpts, opts...) }
}

// WithAPIMapOptions appends to the apimap options applied at Build.
func WithAPIMapOptions(opts ...apimap.Option) Option {
	return func(o *options) { o.apimapOpts = append(o.apimapOpts, opts...) }
}

// WithAPIMapRegistration registers typed request/response models against
// the apimap engine BEFORE Build seals it. Required when using apimap
// with typed Decode/Exchange.
//
//	service.New(... ,
//	    service.WithAPIMapRegistration(func(e *apimap.Engine) {
//	        apimap.RegisterResponse[MicroLinkResp](e, "microlink.metadata")
//	    }),
//	)
func WithAPIMapRegistration(fn func(*apimap.Engine)) Option {
	return func(o *options) { o.apimapRegistration = fn }
}

// WithAPIMapEnv supplies explicit values for ${VAR} substitution in
// apimap's clients.yaml. Map is consulted before os.LookupEnv. Use this
// to feed typed config into apimap without polluting process env.
//
//	service.New(... ,
//	    service.WithAPIMapEnv(map[string]string{"UPSTREAM_URL": cfg.UpstreamURL}),
//	)
func WithAPIMapEnv(m map[string]string) Option {
	return func(o *options) { o.apimapEnv = m }
}

// WithAPIMap enables apimap auto-build using DefaultAPIMapPath when no
// Config.APIMap.Path override is set. Equivalent to setting
// Config.APIMap.Enabled = true. Missing file produces
// CodeAPIMapYAMLNotFound at service.New time.
func WithAPIMap() Option {
	return func(o *options) { o.apimapEnable = true }
}

// WithNATSMapRegistration registers typed subscriber handlers and
// publishers on the natsmap engine BEFORE Build seals it. Required when
// using Config.NATSMap.{Subscribers,Publishers}Path.
//
//	service.New(... ,
//	    service.WithNATSMapRegistration(func(e *natsmap.Engine) {
//	        natsmap.RegisterHandler[OrderCreated](e, "invoice_sender", handle)
//	        natsmap.RegisterPublisher[OrderCreated](e, "orders.created")
//	    }),
//	)
func WithNATSMapRegistration(fn func(*natsmap.Engine)) Option {
	return func(o *options) { o.natsmapRegistration = fn }
}

// WithNATSMapEnv supplies explicit values for ${VAR} substitution in
// natsmap's subscribers/publishers YAML. Map is consulted before
// os.LookupEnv. Symmetric to WithAPIMapEnv.
func WithNATSMapEnv(m map[string]string) Option {
	return func(o *options) { o.natsmapEnv = m }
}

// WithNATSMap enables natsmap auto-build using the default
// subscribers/publishers paths. Equivalent to setting
// Config.NATSMap.Enabled = true. Missing both default files produces
// CodeNATSMapYAMLNotFound; at least one must exist. Requires NATS to
// be configured (Config.NATS.URL set).
func WithNATSMap() Option {
	return func(o *options) { o.natsmapEnable = true }
}

// WithNATSOptions appends to the natsclient options.
func WithNATSOptions(opts ...natsclient.Option) Option {
	return func(o *options) { o.natsOpts = append(o.natsOpts, opts...) }
}

// WithRunOptions appends fibermap.RunOption entries to the default
// production-ops bundle Run uses.
func WithRunOptions(opts ...fibermap.RunOption) Option {
	return func(o *options) { o.runOpts = append(o.runOpts, opts...) }
}

// WithRoutes enables routes auto-load in svc.Run() using
// DefaultRoutesPath. Equivalent to setting Config.Routes.Enabled = true.
// Missing file at Run time produces CodeRoutesYAMLNotFound.
func WithRoutes() Option {
	return func(o *options) { o.routesEnable = true }
}
