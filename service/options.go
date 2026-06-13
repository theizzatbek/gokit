package service

import (
	"io/fs"
	"log/slog"
	"strings"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/robfig/cron/v3"

	"github.com/theizzatbek/gokit/clients/apimap"
	"github.com/theizzatbek/gokit/clients/httpc"
	natsclient "github.com/theizzatbek/gokit/clients/nats"
	"github.com/theizzatbek/gokit/clients/natsmap"
	"github.com/theizzatbek/gokit/clients/natsmap/natsgw"
	"github.com/theizzatbek/gokit/clients/ratelimit"
	redisclient "github.com/theizzatbek/gokit/clients/redis"
	s3client "github.com/theizzatbek/gokit/clients/s3"
	"github.com/theizzatbek/gokit/cronmap"
	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/db/outbox"
	"github.com/theizzatbek/gokit/fibermap"
	"github.com/theizzatbek/gokit/fibermap/bind"
	"github.com/theizzatbek/gokit/fibermap/dev"
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
	corsWired                  bool // flipped by WithCORS / WithCORSConfig; gates env auto-enable in applyEnvDefaults
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
	validator                  bind.Validator            // nil → default validator.New(validator.WithRequiredStructEnabled())
	extraValidators            map[string]validator.Func // tag-name → func; registered on the kit-default validator when WithValidator was NOT passed
	refreshGCInterval          time.Duration             // 0 = disabled (default); > 0 = period between refresh-store GarbageCollect runs
	otelServiceName            string                    // non-empty triggers OpenTelemetry setup at service.New time
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
	bodyLimit                  int                // WithBodyLimit — fiber.Config.BodyLimit override; 0 → fiber default (4 MiB)
	errorHandler               fiber.ErrorHandler // WithErrorHandler — overrides the default fibermap.ErrorHandler when non-nil
	dbOpts                     []db.Option
	skipAutoDBMetrics          bool // WithoutAutoDBMetrics — suppress auto db.WithMetrics(s.metrics)
	otelPgxOpts                []otelkit.PgxTracerOption
	skipOtelPgxTracer          bool
	outboxEnable               bool
	outboxOpts                 []outbox.WorkerOption
	outboxDispatch             outbox.PublishFn
	outboxAutoSchema           bool
	cronJobs                   []CronJob
	cronSlugs                  map[string]string
	cronParser                 cron.Parser
	skipLoggerInjector         bool
	migrationsFS               fs.FS
	skipOutboxReadiness        bool
	outboxCheckerOpts          []outbox.CheckerOption
	skipOtelLogs               bool
	otelLogsOpts               []otelkit.LogsOption
	dbDrainTimeout             time.Duration
	s3Opts                     []s3client.Option
	rateLimitCfg               *ratelimit.Config
	rateLimitOpts              []ratelimit.Option
	preflightEnable            bool
	preflightPath              string
	preflightTimeout           time.Duration
	devEnable                  bool
	devPrefix                  string
	devConfigOpts              []dev.ConfigOption
	natsgwEnable               bool
	natsgwPath                 string
	natsgwOpts                 []natsgw.Option
	webhooksCfg                *WebhooksConfig
	cronMapEnable              bool
	cronMapEnv                 map[string]string
	cronMapHandlers            map[string]cronmap.HandlerFn
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
//
// For the common "kit defaults + one custom tag" case, prefer
// [WithExtraValidators] — it registers tags ON the kit-default
// validator instead of swapping the whole instance.
func WithValidator(v bind.Validator) Option { return func(o *options) { o.validator = v } }

// WithExtraValidators registers additional tag-name → validator.Func
// pairs on the kit-default *validator.Validate that [service.New]
// builds when [WithValidator] was NOT passed. Solves the common
// "kit defaults + one custom tag" case: registering a `safe_url`,
// `username`, or `slug_chars` tag without having to reconstruct the
// kit-default validator from scratch.
//
//	svc, _ := service.New[AppCtx, Claims](ctx, cfg,
//	    service.WithExtraValidators(map[string]validator.Func{
//	        "slug_chars": isSafeSlug,
//	        "safe_url":   isSafeURL,
//	    }))
//
// Multiple WithExtraValidators calls accumulate into a single map;
// later calls overwrite earlier registrations on the same tag name
// (last-write-wins). Empty / nil maps are no-ops.
//
// Interaction with [WithValidator]
//
// WithExtraValidators is meaningful only when WithValidator was NOT
// passed. When both are present, the caller's WithValidator instance
// is used verbatim — the extras are silently ignored, because the
// kit refuses to mutate a caller-supplied validator (it might be
// shared with other call paths in the caller's process and the kit
// can't know what tags are safe to add). If you need both a custom
// validator AND extra tags, register them on your validator
// instance directly before calling WithValidator.
func WithExtraValidators(rules map[string]validator.Func) Option {
	return func(o *options) {
		if len(rules) == 0 {
			return
		}
		if o.extraValidators == nil {
			o.extraValidators = make(map[string]validator.Func, len(rules))
		}
		for tag, fn := range rules {
			o.extraValidators[tag] = fn
		}
	}
}

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
		o.corsWired = true
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
func WithOtel(serviceName string, opts OtelOptions) Option {
	return func(o *options) {
		o.otelServiceName = serviceName
		applyOtelOptions(o, opts)
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
func WithSentry(dsn string, opts SentryOptions) Option {
	return func(o *options) {
		o.sentryDSN = dsn
		applySentryOptions(o, opts)
	}
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

// WithRateLimit opts in the kit's Redis-backed rate limiter and
// registers the `rate_limit_redis` middleware factory on Engine.
// Requires a configured Redis (Config.Redis.URL); otherwise
// service.New returns *errs.Error{Code: CodeRateLimitNeedsRedis}.
//
// The limit + window come from cfg — every YAML route tagged with
// `rate_limit_redis` shares this single budget. To run multiple
// budgets in one service, build limiters manually with
// ratelimit.NewRedis and register additional factories via
// fibermap.RegisterMiddlewareFactory.
//
//	service.New[App, Claims](ctx, cfg,
//	    service.WithRateLimit(ratelimit.Config{
//	        KeyPrefix: "rl:",
//	        Limit:     120,
//	        Window:    time.Minute,
//	    }))
//
// When Auth is also configured, the `user` / `subject` YAML key
// strategy uses auth.KeyBySubject[C] automatically — no further
// wiring needed.
func WithRateLimit(cfg ratelimit.Config, opts ...ratelimit.Option) Option {
	return func(o *options) {
		o.rateLimitCfg = &cfg
		o.rateLimitOpts = append(o.rateLimitOpts, opts...)
	}
}

// WithPreflightEndpoint mounts the `/preflight` HTTP endpoint that
// renders [PreflightResult] as JSON (200 on success, 503 on any
// failure). Used by `kit doctor` for ops smoke-tests and by CI
// pipelines that need a "is staging actually ready" gate before
// running integration tests.
//
// Path is configurable; pass "" to use the default "/preflight".
//
// Unlike `/readyz` (auto-mounted, K8s readiness probe), preflight
// is opt-in — it's a debug / ops surface, not part of the request
// path. Don't wire it as a K8s readinessProbe unless you've raised
// the probe timeout to accommodate slower checks.
func WithPreflightEndpoint(path string) Option {
	return func(o *options) {
		o.preflightEnable = true
		o.preflightPath = path
	}
}

// WithPreflightTimeout caps how long Preflight waits on the slowest
// check. Default 10s — chosen to accommodate slower one-shot
// validations (S3 HEAD across regions, schema-version SELECT).
//
// Tighten for fast services; raise when including legitimately slow
// custom checkers via [WithReadinessChecker].
func WithPreflightTimeout(d time.Duration) Option {
	return func(o *options) { o.preflightTimeout = d }
}

// WithS3Options appends to the s3client options applied by
// service.New. Logger + Metrics are already auto-wired (so S3
// observability lands on the shared service registry); use this
// for niche overrides — custom retry policy via the AWS SDK
// config, etc.
func WithS3Options(opts ...s3client.Option) Option {
	return func(o *options) { o.s3Opts = append(o.s3Opts, opts...) }
}

// WithDBDrainTimeout caps the wait for in-flight DB queries /
// transactions during Service.Close. Default 5s — accommodates a
// burst of normal handlers without holding the SIGTERM-deadline
// hostage to a stuck query.
//
// Set to 0 to keep the default; a positive value overrides it.
// `Service.Close` calls `svc.DB.Drain(ctx)` with this deadline
// before falling through to a hard `Close()`, so long-running
// handlers finish their work cleanly instead of getting cut
// mid-transaction.
//
// service.Run already plumbs `WithShutdownTimeout(...)` for the
// HTTP server; this is the DB-side counterpart.
func WithDBDrainTimeout(d time.Duration) Option {
	return func(o *options) { o.dbDrainTimeout = d }
}

// WithDBOptions appends to the db options applied by service.New.
// `db.WithLogger` and `db.WithMetrics` are already wired
// automatically (the latter when [WithMetrics] is also configured —
// see [WithoutAutoDBMetrics] for the opt-out). Use this for
// `db.WithSlowQueryThreshold`, additional `db.WithTracer` calls
// (e.g. plugging an audit tracer alongside the OTel one auto-
// installed by [WithOtel]), `db.WithReadLagBudget`,
// `db.WithReplicaLagPolling`, or any future db option the kit
// grows.
//
// DO NOT pass `db.WithMetrics(reg)` here unless you ALSO pass
// [WithoutAutoDBMetrics] — the kit's auto-wiring + this duplicate
// registers the same collectors twice on the same registry and
// `prometheus.MustRegister` panics. The convenient pattern for
// unified scraping is to call only `service.WithMetrics(reg)` and
// rely on the auto-wire.
func WithDBOptions(opts ...db.Option) Option {
	return func(o *options) { o.dbOpts = append(o.dbOpts, opts...) }
}

// WithoutAutoDBMetrics opts out of the kit-default auto-wiring of
// `db.WithMetrics(s.metrics)`. Use when:
//
//   - The service wants its db metrics on a DIFFERENT registry than
//     the one passed to [WithMetrics]. Pair this opt-out with
//     `WithDBOptions(db.WithMetrics(otherReg))`.
//   - The service explicitly does not want db_* series on /metrics
//     (rare; the kit-default scrape is the canonical path).
//
// Without this opt the kit prepends `db.WithMetrics(s.metrics)` to
// the dbOpts slice so db_query_duration_seconds + friends land on
// the same registry as the rest of the kit. The op-out is a flag,
// not a wrap — calling [WithDBOptions] separately to pass
// `db.WithMetrics(s.metrics)` while ALSO not opting out PANICS at
// Connect time via duplicate `prometheus.MustRegister`.
func WithoutAutoDBMetrics() Option {
	return func(o *options) { o.skipAutoDBMetrics = true }
}

// WithOtelPgxOptions configures the OTel pgx tracer that
// [WithOtel] auto-attaches to the DB pool. Forwards options to
// [otelkit.NewPgxTracer]: WithPgxTracerName, WithPgxSpanNamer,
// WithoutPgxSQL, WithPgxMaxSQLLength.
//
//	service.WithOtel("orders-api"),
//	service.WithOtelPgxOptions(
//	    otelkit.WithoutPgxSQL(), // PII in WHERE clauses
//	),
//
// No-op without [WithOtel] or when [WithoutOtelPgxTracer] is also
// passed.
func WithOtelPgxOptions(opts ...otelkit.PgxTracerOption) Option {
	return func(o *options) { o.otelPgxOpts = append(o.otelPgxOpts, opts...) }
}

// WithoutOtelPgxTracer suppresses the auto-wired OTel pgx tracer
// that [WithOtel] otherwise installs. Tracing on the HTTP path
// (otelfiber, otelhttp) stays on; only DB query spans are
// disabled. Use when DB tracing is provided by a different layer
// (proxy/sidecar) or when span volume from per-query traces would
// blow the export budget.
func WithoutOtelPgxTracer() Option {
	return func(o *options) { o.skipOtelPgxTracer = true }
}

// WithoutLoggerInjector suppresses the auto-installed
// [fibermap.LoggerInjector] middleware. By default service.New
// installs it at the App level so handlers can call
// `fibermap.LoggerFrom(c)` and get a *slog.Logger pre-bound with
// `method`, `path`, `request_id`, `user_id` (when authenticated),
// and `route` (when set).
//
// Disable when:
//   - You install your own request-scoped logger middleware.
//   - You don't care about the per-request enrichment and want to
//     keep the middleware chain minimal.
func WithoutLoggerInjector() Option {
	return func(o *options) { o.skipLoggerInjector = true }
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

// WithErrorHandler overrides the default fiber.Config.ErrorHandler.
// The kit's default is [fibermap.ErrorHandler] (renders *errs.Error
// returns as the kit's `{code, message, details[]}` JSON wire shape),
// installed unconditionally since v1.0.1 regardless of [WithBodyLimit].
//
// Pass a wrapper to layer cross-cutting behaviour on top of the kit
// default — the typical case is sentrykit 5xx auto-capture:
//
//	svc, _ := service.New[AppCtx, Claims](ctx, cfg,
//	    service.WithSentry(dsn, sentryOpts),
//	    service.WithErrorHandler(
//	        sentrykit.WrapErrorHandler(fibermap.ErrorHandler(logger))))
//
// `sentrykit.WrapErrorHandler` reports the error to the
// per-request Sentry hub BEFORE delegating to the supplied
// fibermap.ErrorHandler for the actual HTTP response. Without this
// option, the kit's auto-installed default ErrorHandler runs
// directly — *errs.Error still maps to the right JSON shape, but
// Sentry sees nothing.
//
// Pass nil to keep the kit default (equivalent to never calling
// WithErrorHandler at all). Caller-supplied
// [fibermap.WithFiberConfig] via [WithRunOptions] still wins over
// this option (it's applied later in the RunOption chain) — use
// either WithErrorHandler OR a custom fiber.Config, not both.
func WithErrorHandler(h fiber.ErrorHandler) Option {
	return func(o *options) { o.errorHandler = h }
}

// WithMigrations applies the migrations bundled into fsys via
// [migrate.Up] right after the DB pool is built and before any
// subsystem that reads schema (auth.refreshpg, outbox, apikeypg).
// Files use the `NNNN_name.sql` convention; see the db/migrate
// README for details on the schema_migrations tracking table and
// the optional `-- @migrate:no-transaction` directive.
//
//	//go:embed migrations/*.sql
//	var migrationsFS embed.FS
//
//	svc, _ := service.New(ctx, cfg,
//	    service.WithMigrations(migrationsFS))
//
// Migration failures surface from service.New with the original
// *errs.Error from db/migrate (CodeApplyFailed, CodeBootstrapFailed,
// etc) — no extra wrapping.
//
// Off by default — operators who run a separate migration tool
// (golang-migrate, goose) skip this option and apply schema before
// process start.
func WithMigrations(fsys fs.FS) Option {
	return func(o *options) { o.migrationsFS = fsys }
}

// WithOutboxReadinessOpts forwards [outbox.CheckerOption] values
// to the readiness check that [WithOutbox] auto-installs. Tune
// depth / lag thresholds, override the check name.
//
//	service.WithOutbox(...),
//	service.WithOutboxReadinessOpts(
//	    outbox.WithMaxDepth(50000),
//	    outbox.WithMaxLag(time.Hour),
//	),
//
// No effect without [WithOutbox] or when
// [WithoutOutboxReadiness] is also passed.
func WithOutboxReadinessOpts(opts ...outbox.CheckerOption) Option {
	return func(o *options) { o.outboxCheckerOpts = append(o.outboxCheckerOpts, opts...) }
}

// WithoutOutboxReadiness disables the auto-installed outbox
// readiness check. Use when the deployment has its own SLA
// monitoring for outbox lag and doesn't want /readyz to flap on
// transient backlog.
func WithoutOutboxReadiness() Option {
	return func(o *options) { o.skipOutboxReadiness = true }
}

// WithOutbox enables the transactional outbox worker. Requires
// both DB and NATSMap to also be configured — without NATSMap the
// default PublishFn has nothing to dispatch to (use
// [WithOutboxDispatcher] to plug a different bus).
//
// Auto-wires:
//   - Worker construction with the unified service logger + metrics
//     registry (zero footprint when neither is set).
//   - OnShutdown(Stop) so Service.Close waits for the drain to
//     finish before tearing down the DB pool.
//   - service.metrics → outbox.WithMetrics so outbox_* series land
//     on the same /metrics endpoint as the rest of the kit.
//
// Default PublishFn: `natsmap.PublishRaw(ctx, rt, e.EventType,
// e.Payload, e.Headers)`. Override via [WithOutboxDispatcher] for
// non-natsmap buses or to add per-event side effects (audit log,
// fan-out to multiple subjects).
//
//	service.New(... ,
//	    service.WithNATSMap(),
//	    service.WithOutbox(
//	        outbox.WithInterval(10*time.Second),
//	        outbox.WithRetention(7*24*time.Hour),
//	    ))
func WithOutbox(opts ...outbox.WorkerOption) Option {
	return func(o *options) {
		o.outboxEnable = true
		o.outboxOpts = append(o.outboxOpts, opts...)
	}
}

// WithOutboxDispatcher overrides the default PublishFn (which
// wraps natsmap.PublishRaw). Use to dispatch to a non-natsmap bus,
// to fan out one event onto multiple subjects, or to wrap the
// natsmap publish with per-event side effects (audit log, metric
// tag).
//
//	service.WithOutboxDispatcher(func(ctx context.Context, e outbox.Event) error {
//	    if err := natsmap.PublishRaw(ctx, svc.NATSMap, e.EventType, e.Payload, e.Headers); err != nil {
//	        return err
//	    }
//	    return auditLog.Write(ctx, e)
//	})
func WithOutboxDispatcher(fn outbox.PublishFn) Option {
	return func(o *options) { o.outboxDispatch = fn }
}

// WithOutboxAutoSchema applies [outbox.Schema] via svc.DB.Exec at
// Service.New time. Convenience for callers that don't run a
// dedicated migration tool — the kit's schema.sql is idempotent
// (CREATE TABLE IF NOT EXISTS + ALTER ADD COLUMN IF NOT EXISTS)
// so repeated boots stay safe.
//
// Off by default to keep schema management in the operator's
// hands; explicitly enable on local dev / smoke tests where the
// migration burden isn't worth the boilerplate.
func WithOutboxAutoSchema() Option {
	return func(o *options) { o.outboxAutoSchema = true }
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
