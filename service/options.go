package service

import (
	"log/slog"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/theizzatbek/gokit/clients/apimap"
	"github.com/theizzatbek/gokit/clients/httpc"
	natsclient "github.com/theizzatbek/gokit/clients/nats"
	"github.com/theizzatbek/gokit/clients/natsmap"
	redisclient "github.com/theizzatbek/gokit/clients/redis"
	"github.com/theizzatbek/gokit/fibermap"
	"github.com/theizzatbek/gokit/fibermap/bind"
	"github.com/theizzatbek/gokit/fibermap/openapi"
	"github.com/theizzatbek/gokit/otelkit"
	"github.com/theizzatbek/gokit/sentrykit"
)

// Option configures service.New beyond what Config covers.
type Option func(*options)

type options struct {
	logger                     *slog.Logger
	metrics                    prometheus.Registerer
	openapiEnable              bool // WithOpenAPI() flips this
	openapiOpts                []openapi.Option
	fiberMiddleware            []fiber.Handler
	skipBearerLayer            bool
	httpcOpts                  []httpc.Option
	apimapOpts                 []apimap.Option
	apimapRegistration         func(*apimap.Engine)
	apimapEnv                  map[string]string
	apimapEnable               bool
	natsmapRegistration        func(*natsmap.Engine)
	natsmapEnv                 map[string]string
	natsmapEnable              bool
	natsOpts                   []natsclient.Option
	redisOpts                  []redisclient.Option
	routesEnable               bool
	runOpts                    []fibermap.RunOption
	skipConnectRetry           bool
	validator                  bind.Validator // nil → default validator.New(validator.WithRequiredStructEnabled())
	refreshGCInterval          time.Duration  // 0 = disabled (default); > 0 = period between refresh-store GarbageCollect runs
	otelServiceName            string         // non-empty triggers OpenTelemetry setup at service.New time
	otelOpts                   []otelkit.Option
	otelMetricsOpts            []otelkit.MetricsOption
	skipOtelMetrics            bool // suppress the Prometheus→OTel metrics bridge even when WithOtel is set
	skipRuntimeMetrics         bool // suppress Go runtime + process collector auto-registration
	sentryDSN                  string
	sentryOpts                 []sentrykit.Option
	sentrySlogOpts             []sentrykit.HandlerOption
	skipSentryUserScope        bool
	sentryRefreshGCSlug        string
	skipSentryRefreshGCMonitor bool
	skipReadiness              bool               // WithoutReadiness — suppress auto /readyz
	readinessPath              string             // override default "/readyz"
	readinessTimeout           time.Duration      // forwarded to fibermap.WithReadinessOpts; 0 → default
	readinessExtraCheckers     []fibermap.Checker // app-level checkers appended after kit-wired subsystems
	skipSecurityHeaders        bool               // WithoutSecurityHeaders — suppress auto OWASP headers
	securityHeaderOpts         []fibermap.SecurityHeadersOption
	bodyLimit                  int // WithBodyLimit — fiber.Config.BodyLimit override; 0 → fiber default (4 MiB)
}

// WithLogger overrides the auto-built slog.Logger.
func WithLogger(l *slog.Logger) Option { return func(o *options) { o.logger = l } }

// WithValidator overrides the default request validator installed on
// the engine. The default is
// `validator.New(validator.WithRequiredStructEnabled())` from
// go-playground/validator/v10 — sufficient for stock tags like
// `validate:"required,min=3,email"`. Pass a customised instance to
// register additional struct- or field-level validators:
//
//	v := validator.New(validator.WithRequiredStructEnabled())
//	v.RegisterValidation("safe_url", isSafeURL)
//	svc, _ := service.New[AppCtx, Claims](ctx, cfg, service.WithValidator(v))
//
// The argument type is bind.Validator (any type satisfying
// `Struct(any) error`) so custom non-validator/v10 implementations work
// too. Pass nil to keep the default.
func WithValidator(v bind.Validator) Option { return func(o *options) { o.validator = v } }

// WithMetrics overrides the default prometheus.NewRegistry().
func WithMetrics(reg prometheus.Registerer) Option {
	return func(o *options) { o.metrics = reg }
}

// WithOpenAPI enables OpenAPI mounting (/openapi.json + /docs). When
// routes.yaml contains an `openapi:` block, its Info/Servers/
// SecuritySchemes/MiddlewareSecurity populate the document; opts
// passed here apply on top (Info: last-write-wins; Servers /
// SecuritySchemes / MiddlewareSecurity: accumulating append).
//
// Calling WithOpenAPI() with no opts is the typical YAML-driven case.
// Pass openapi.WithInfo(...) / WithServer(...) / WithSecurity(...) /
// MapMiddlewareToSecurity(...) / WithDefaultResponse(...) to override
// or augment from code.
//
// OpenAPI also auto-mounts when routes.yaml contains an openapi: block,
// even if WithOpenAPI is not called explicitly.
func WithOpenAPI(opts ...openapi.Option) Option {
	return func(o *options) {
		o.openapiEnable = true
		o.openapiOpts = append(o.openapiOpts, opts...)
	}
}

// WithFiberMiddleware appends fiber-level middleware installed BEFORE
// the engine's contextInit. The auto-installed Bearer-optional layer
// stays unless WithoutBearerOptionalLayer is also passed.
func WithFiberMiddleware(handlers ...fiber.Handler) Option {
	return func(o *options) { o.fiberMiddleware = append(o.fiberMiddleware, handlers...) }
}

// WithCORS installs Fiber's CORS middleware at the App level with
// kit-sensible defaults: allowed methods cover the standard REST set;
// allowed headers include Authorization, Content-Type, X-Request-ID,
// and X-Idempotency-Key; X-Request-ID is exposed back to browsers;
// MaxAge is 24h.
//
// AllowCredentials is enabled when every origin is explicit (e.g.
// "https://app.example.com"), and DISABLED automatically when "*" is
// listed — per the CORS spec, browsers reject `Access-Control-Allow-
// Origin: *` together with credentials.
//
//	svc, _ := service.New[AppCtx, Claims](ctx, cfg,
//	    service.WithCORS("https://app.example.com", "https://admin.example.com"))
//
// For full control over Headers / ExposeHeaders / MaxAge / Next /
// AllowOriginsFunc — use [WithCORSConfig].
func WithCORS(origins ...string) Option {
	cfg := cors.Config{
		AllowOrigins:  strings.Join(origins, ","),
		AllowMethods:  "GET,POST,PUT,PATCH,DELETE,OPTIONS",
		AllowHeaders:  "Origin,Content-Type,Accept,Authorization,X-Request-ID,X-Idempotency-Key",
		ExposeHeaders: "X-Request-ID",
		MaxAge:        86400,
	}
	if !containsWildcardOrigin(origins) {
		cfg.AllowCredentials = true
	}
	return WithCORSConfig(cfg)
}

// WithCORSConfig is the explicit-config variant of [WithCORS]. The
// supplied cors.Config is handed straight to cors.New — no defaults
// are layered on top, so configure every field you care about
// (especially AllowOrigins / AllowMethods / AllowHeaders).
//
//	svc, _ := service.New[AppCtx, Claims](ctx, cfg,
//	    service.WithCORSConfig(cors.Config{
//	        AllowOriginsFunc: func(origin string) bool { return strings.HasSuffix(origin, ".example.com") },
//	        AllowCredentials: true,
//	        AllowHeaders:     "Authorization,Content-Type",
//	    }))
func WithCORSConfig(cfg cors.Config) Option {
	return func(o *options) {
		o.fiberMiddleware = append(o.fiberMiddleware, cors.New(cfg))
	}
}

// containsWildcardOrigin returns true when "*" appears anywhere in
// origins — used to suppress AllowCredentials.
func containsWildcardOrigin(origins []string) bool {
	for _, o := range origins {
		if strings.TrimSpace(o) == "*" {
			return true
		}
	}
	return false
}

// WithoutBearerOptionalLayer skips installing auth.Bearer(BearerOptional)
// at the fiber.App level. Only sensible if you have no auth or want to
// orchestrate the layer yourself.
func WithoutBearerOptionalLayer() Option {
	return func(o *options) { o.skipBearerLayer = true }
}

// WithOtel enables OpenTelemetry tracing across the service.
//
//   - Initializes a TracerProvider via OTLP/HTTP using [otelkit.Setup].
//     Configure the exporter endpoint and headers through the OTel-standard
//     OTEL_EXPORTER_OTLP_* environment variables.
//
//   - Installs otelfiber middleware at the App level — every incoming
//     request becomes the root span of its trace, with route name +
//     status as attributes.
//
//   - Wraps httpc's base transport in otelhttp — every outbound call
//     emits a CLIENT span and propagates W3C TraceContext headers.
//     Each retry attempt is its own span.
//
//   - Registers the TracerProvider shutdown via [Service.OnShutdown]
//     so a clean Close flushes pending spans before tearing down.
//
//     svc, _ := service.New[AppCtx, Claims](ctx, cfg,
//     service.WithOtel("orders-api",
//     otelkit.WithServiceVersion("1.0.0"),
//     otelkit.WithSampleRatio(0.1)))
//
// serviceName populates service.name on every span. Pass empty / omit
// the option to leave tracing disabled (the default).
//
// Database query tracing (pgx) is out of scope here — add a pgx tracer
// manually via the db package's options if you need DB spans.
func WithOtel(serviceName string, opts ...otelkit.Option) Option {
	return func(o *options) {
		o.otelServiceName = serviceName
		o.otelOpts = opts
	}
}

// WithSentry enables Sentry error tracking. dsn is required — an
// empty DSN trips a sentrykit.Setup error at service.New (refusing
// to silently disable misconfiguration). Service auto-installs:
//
//   - sentrykit.FiberMiddleware as a user-chain middleware. The
//     middleware clones a per-request sentry.Hub, populates HTTP
//     scope (method/route/headers/IP/request_id), and captures
//     panics before re-panicking so fibermap.Recover still owns the
//     500 response.
//   - sentrykit.Flush via OnShutdown so a clean Close ships pending
//     events.
//
// 5xx auto-capture requires explicit error-handler wiring — wrap the
// service's fiber.Config.ErrorHandler with sentrykit.WrapErrorHandler.
// Service does NOT auto-wrap because not every caller sets a custom
// error handler.
//
//	svc, _ := service.New[AppCtx, Claims](ctx, cfg,
//	    service.WithSentry(cfg.SentryDSN,
//	        sentrykit.WithEnvironment(cfg.Env),
//	        sentrykit.WithRelease(buildSHA),
//	        sentrykit.WithTag("region", cfg.Region)))
//
// When both WithOtel and WithSentry are set, otelfiber is the
// outermost user-chain layer (prepended by setupOtel) and sentry
// sits inside it — so every captured event implicitly shares the
// trace_id of the surrounding OTel span.
func WithSentry(dsn string, opts ...sentrykit.Option) Option {
	return func(o *options) {
		o.sentryDSN = dsn
		o.sentryOpts = opts
	}
}

// WithSentryRefreshGCSlug overrides the default "kit-refresh-gc"
// monitor slug used when the refresh-token GC ticker reports
// check-ins to Sentry Crons. Use to distinguish multiple kit-based
// services sharing one Sentry project, e.g. "orders-refresh-gc".
//
// No effect unless [WithSentry] and [WithRefreshGC] are both set
// (cron monitoring lights up only when both subsystems are wired).
func WithSentryRefreshGCSlug(slug string) Option {
	return func(o *options) { o.sentryRefreshGCSlug = slug }
}

// WithoutSentryRefreshGCMonitor disables Sentry Crons check-ins for
// the refresh-token GC ticker. Use in multi-replica deployments
// where every replica ticks on its own — Sentry doesn't deduplicate
// by slug, so one configured monitor would receive one heartbeat
// per replica per tick (the "Failing job" alert never fires, the
// "Missed check-in" alert is impossible to tune).
//
// Tracing / breadcrumbs / error capture from PRs #1–#4 stay
// enabled; only the periodic check-in is suppressed.
func WithoutSentryRefreshGCMonitor() Option {
	return func(o *options) { o.skipSentryRefreshGCMonitor = true }
}

// WithoutSentryUserScope disables the per-request user scope that
// service.WithSentry otherwise installs (when Auth is also wired).
//
// Default behaviour: when a request is authenticated, every Sentry
// event captured during that request carries
// `sentry.User{ID: principal.Subject}` — visible in the Sentry UI as
// the "Affected User" facet. Disable when:
//
//   - Subject is considered PII in your deployment (e.g. it's the
//     user's email).
//   - You want to set User scope manually from handlers (e.g. with a
//     hashed/redacted Subject).
//
// No effect unless WithSentry is also set or Auth is unconfigured.
func WithoutSentryUserScope() Option {
	return func(o *options) { o.skipSentryUserScope = true }
}

// WithSentryErrorCapture enables Sentry event auto-capture for log
// records at >= level. Forwards to
// [sentrykit.WithCaptureLevel]: when the kit-built logger emits a
// record at or above the threshold, an event ships in addition to
// the breadcrumb. When the record carries an attr named "err",
// "error", or "cause" with an error value, the event is a Sentry
// Exception (stack frames from the running goroutine); otherwise
// it's a Message event.
//
//	service.WithSentry(dsn),
//	service.WithSentryErrorCapture(slog.LevelError)
//
// No-op without [WithSentry] or when the caller supplied their own
// logger via WithLogger (user loggers are kept untouched). Duplicate
// events for the same (level, category, message) within 60s are
// suppressed; override with
// service.WithSentryBreadcrumbs(sentrykit.WithCaptureDedupeWindow(d)).
func WithSentryErrorCapture(level slog.Level) Option {
	return func(o *options) {
		o.sentrySlogOpts = append(o.sentrySlogOpts, sentrykit.WithCaptureLevel(level))
	}
}

// WithSentryBreadcrumbs configures the slog→breadcrumb bridge that
// service auto-installs on the kit-built logger when [WithSentry] is
// passed. Forwarded to [sentrykit.SlogHandler]: WithDebugBreadcrumbs,
// WithAttrFilter, WithCategoryAttr, WithMaxBreadcrumbValueLen,
// WithCaptureDedupeWindow, WithCaptureErrorAttrKeys.
//
//	service.New(... ,
//	    service.WithSentry(dsn),
//	    service.WithSentryBreadcrumbs(
//	        sentrykit.WithAttrFilter(func(k string) bool { return k != "sql" }),
//	        sentrykit.WithMaxBreadcrumbValueLen(256),
//	    ))
//
// No-op when WithSentry was not passed or when the caller supplied
// their own logger via WithLogger (user loggers are kept as-is to
// avoid surprising side effects on a pre-tuned log pipeline).
func WithSentryBreadcrumbs(opts ...sentrykit.HandlerOption) Option {
	return func(o *options) { o.sentrySlogOpts = append(o.sentrySlogOpts, opts...) }
}

// WithOtelMetricsOptions configures the OTel metrics pipeline that
// service.WithOtel auto-enables. Forwarded to [otelkit.SetupMetrics]
// (interval, exporter options, resource attributes).
//
//	service.WithOtel("orders-api"),
//	service.WithOtelMetricsOptions(
//	    otelkit.WithMetricsInterval(15*time.Second),
//	),
//
// No-op unless WithOtel is also passed.
func WithOtelMetricsOptions(opts ...otelkit.MetricsOption) Option {
	return func(o *options) { o.otelMetricsOpts = append(o.otelMetricsOpts, opts...) }
}

// WithoutOtelMetrics suppresses the Prometheus→OTel metrics bridge
// that WithOtel otherwise auto-enables. Use when the deployment
// scrapes the kit's /metrics endpoint directly and doesn't want a
// second push pipeline, or when the chosen OTel backend already
// scrapes Prometheus and would receive duplicates.
//
// Tracing stays on; only the metrics bridge is skipped.
func WithoutOtelMetrics() Option {
	return func(o *options) { o.skipOtelMetrics = true }
}

// WithRefreshGC schedules periodic garbage collection of expired
// refresh tokens against the refresh store wired through Auth. Without
// it, the underlying table (auth_refresh_tokens for refreshpg) grows
// forever — even though expired entries no longer authenticate
// anything, they cost storage and slow Consume's diagnostic SELECT.
//
//	service.WithRefreshGC(15 * time.Minute)
//
// Calls Store.GarbageCollect(ctx, time.Now()) on each tick. Counts go
// to the service logger at INFO; errors at WARN. The goroutine is
// registered with [Service.OnShutdown] so a clean [Service.Close]
// stops it before the DB connection closes.
//
// interval <= 0 disables the feature (same as not calling the option).
// Service.Auth must be configured for the GC to start; otherwise the
// option is a no-op.
func WithRefreshGC(interval time.Duration) Option {
	return func(o *options) { o.refreshGCInterval = interval }
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

// WithRedisOptions appends to the redisclient options applied by
// service.New (logger + metrics are auto-applied). Use to set
// custom redis.Options fields (PoolSize, TLSConfig, ...) via
// redisclient.WithRedisOptions.
func WithRedisOptions(opts ...redisclient.Option) Option {
	return func(o *options) { o.redisOpts = append(o.redisOpts, opts...) }
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

// WithoutRuntimeMetrics suppresses auto-registration of the Go
// runtime and process collectors on the service registry. By default
// service.New registers `collectors.NewGoCollector()` and
// `collectors.NewProcessCollector(ProcessCollectorOpts{})` so a
// scrape returns goroutine count, heap stats, GC pause histograms,
// FD count, RSS, and CPU seconds out of the box.
//
// Useful when the caller already registered these collectors on the
// shared registry (avoids prometheus.AlreadyRegisteredError) or when
// the user wants the registry to contain only kit/app series.
func WithoutRuntimeMetrics() Option {
	return func(o *options) { o.skipRuntimeMetrics = true }
}

// WithoutReadiness suppresses the auto-installed /readyz endpoint.
// By default service auto-mounts /readyz that pings every wired
// subsystem (DB, NATS, Redis) in parallel — pass this option when
// the deployment uses a custom readiness probe or doesn't want one.
//
// Liveness (/healthz) is unaffected — it always stays on unless
// explicitly disabled via fibermap.WithoutHealthCheck through
// [WithRunOptions].
func WithoutReadiness() Option {
	return func(o *options) { o.skipReadiness = true }
}

// WithReadinessPath overrides the default "/readyz" mount point.
// Set to a custom path when an upstream proxy or LB expects a
// specific readiness URL.
func WithReadinessPath(path string) Option {
	return func(o *options) { o.readinessPath = path }
}

// WithReadinessTimeout sets the deadline for the full set of
// checkers run by the auto-mounted /readyz handler. Each Checker
// receives this ctx, so a slow DB doesn't block on stalled NATS
// indefinitely. 0 → fibermap's built-in default (5s).
func WithReadinessTimeout(d time.Duration) Option {
	return func(o *options) { o.readinessTimeout = d }
}

// WithReadinessChecker appends app-level checkers to the
// auto-wired subsystem set (DB, NATS, Redis). Each checker must
// satisfy `fibermap.Checker` — `Name() string` + `Check(ctx) error`.
// Use for migration probes, cache warmup gates, external API
// pings the service must clear before serving traffic.
//
//	svc.WithReadinessChecker(
//	    migrate.NewChecker(svc.DB),
//	    cache.NewWarmupChecker(svc.Redis),
//	)
func WithReadinessChecker(c ...fibermap.Checker) Option {
	return func(o *options) { o.readinessExtraCheckers = append(o.readinessExtraCheckers, c...) }
}

// WithoutSecurityHeaders suppresses the auto-installed OWASP
// security headers middleware (HSTS, X-Content-Type-Options,
// X-Frame-Options, Referrer-Policy, CSP). Use when the headers
// are handled upstream (CDN, reverse proxy) or when the service
// is internal-only and the operator has decided the cost of the
// extra headers isn't worth paying.
func WithoutSecurityHeaders() Option {
	return func(o *options) { o.skipSecurityHeaders = true }
}

// WithSecurityHeaders configures the auto-installed OWASP
// security headers middleware. Forwards any [fibermap.SecurityHeadersOption]
// — e.g. [fibermap.WithHSTSIncludeSubdomains], [fibermap.WithCSP],
// [fibermap.WithoutHSTS]. The middleware is installed regardless;
// pass [WithoutSecurityHeaders] instead to suppress it entirely.
//
//	service.New(... ,
//	    service.WithSecurityHeaders(
//	        fibermap.WithHSTSIncludeSubdomains(),
//	        fibermap.WithCSP("default-src 'self'; script-src 'self' 'unsafe-inline'"),
//	    ))
func WithSecurityHeaders(opts ...fibermap.SecurityHeadersOption) Option {
	return func(o *options) { o.securityHeaderOpts = append(o.securityHeaderOpts, opts...) }
}

// WithBodyLimit overrides Fiber's default request-body limit
// (4 MiB) with the supplied byte count. Fiber returns 413
// Request Entity Too Large when an inbound request exceeds the
// limit — set this tight when the service only accepts small
// JSON payloads to blunt accidental or malicious oversize POSTs.
//
//	service.WithBodyLimit(64 * 1024) // 64 KiB cap
//
// Pass 0 to keep Fiber's default. When the caller also supplies
// a fiber.Config via [WithRunOptions] / [fibermap.WithFiberConfig],
// the caller's config wins (it's applied later in the RunOption
// chain).
func WithBodyLimit(bytes int) Option {
	return func(o *options) { o.bodyLimit = bytes }
}

// WithoutConnectRetry disables the auto-injected K8s-friendly retry
// defaults for DB and NATS Connect calls. Use when the deployment
// strictly orders dependencies (e.g. init-containers) and prefers
// fast-fail diagnostics over patience.
//
// Without this option, service.New auto-defaults ConnectMaxRetries=5,
// ConnectBackoffBase=1s, ConnectBackoffMax=16s for both DB and NATS
// when the cfg values are zero. Setting any cfg field to a non-zero
// value preserves the explicit value; setting ConnectMaxRetries=-1
// via env disables retry without needing this option.
func WithoutConnectRetry() Option {
	return func(o *options) { o.skipConnectRetry = true }
}
