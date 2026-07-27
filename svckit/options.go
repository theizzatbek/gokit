package svckit

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

	"github.com/theizzatbek/gokit/clients/httpc"
	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/fibermap"
	"github.com/theizzatbek/gokit/fibermap/bind"
	"github.com/theizzatbek/gokit/fibermap/dev"
	"github.com/theizzatbek/gokit/fibermap/openapi"
)

// Option configures New beyond what Config covers.
type Option func(*options)

type options struct {
	// core
	logger              *slog.Logger
	metrics             prometheus.Registerer
	validator           bind.Validator
	extraValidators     map[string]validator.Func
	dbOpts              []db.Option
	skipAutoDBMetrics   bool
	skipConnectRetry    bool
	skipRuntimeMetrics  bool
	dbDrainTimeout      time.Duration
	migrationsFS        fs.FS
	routesEnable        bool
	openapiEnable       bool
	openapiOpts         []openapi.Option
	corsHandler         fiber.Handler
	corsWired           bool
	securityHeaderOpts  []fibermap.SecurityHeadersOption
	skipSecurityHeaders bool
	skipBearerLayer     bool
	skipLoggerInjector  bool
	errorHandler        fiber.ErrorHandler
	bodyLimit           int
	refreshGCInterval   time.Duration
	skipReadiness       bool
	readinessPath       string
	readinessTimeout    time.Duration
	preflightEnable     bool
	preflightPath       string
	preflightTimeout    time.Duration
	tlsCert             string
	tlsKey              string

	// cron.go / singleton_cron.go
	cronJobs   []CronJob
	cronParser cron.Parser

	// devmode.go
	devEnable     bool
	devPrefix     string
	devConfigOpts []dev.ConfigOption

	// accumulators: written to both by options above and by mods
	// through Host during Setup/Build/Wire
	mods              []Mod
	httpcOpts         []httpc.Option
	fiberMiddleware   []fiber.Handler
	runOpts           []fibermap.RunOption
	readinessCheckers []fibermap.Checker
	modFactories      map[string]FactoryFunc
	cronWrappers      []func(JobFn) JobFn
	shutdownFns       []func() error
	bootWarnings      []string
}

// WithMod connects a mod. Usually called not directly but through the
// mod's own handle, e.g. s3mod.New(cfg.S3).Option().
func WithMod(m Mod) Option {
	return func(o *options) {
		if m != nil {
			o.mods = append(o.mods, m)
		}
	}
}

// WithLogger overrides the auto-built slog.Logger.
func WithLogger(l *slog.Logger) Option { return func(o *options) { o.logger = l } }

// WithMetrics overrides the default prometheus.NewRegistry().
func WithMetrics(reg prometheus.Registerer) Option {
	return func(o *options) { o.metrics = reg }
}

// WithoutRuntimeMetrics suppresses auto-registration of the Go
// runtime and process collectors on the service registry. By default
// New registers `collectors.NewGoCollector()` and
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

// WithValidator overrides the default request validator installed on
// the engine. The default is
// `validator.New(validator.WithRequiredStructEnabled())` from
// go-playground/validator/v10 — sufficient for stock tags like
// `validate:"required,min=3,email"`. Pass a customised instance to
// register additional struct- or field-level validators:
//
//	v := validator.New(validator.WithRequiredStructEnabled())
//	v.RegisterValidation("safe_url", isSafeURL)
//	h, _ := svckit.New(ctx, cfg, svckit.WithValidator(v))
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
// pairs on the kit-default *validator.Validate that [New] builds when
// [WithValidator] was NOT passed. Solves the common "kit defaults +
// one custom tag" case: registering a `safe_url`, `username`, or
// `slug_chars` tag without having to reconstruct the kit-default
// validator from scratch.
//
//	h, _ := svckit.New(ctx, cfg,
//	    svckit.WithExtraValidators(map[string]validator.Func{
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
// validator AND extra tags, register them on your validator instance
// directly before calling WithValidator.
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

// WithFiberMiddleware appends fiber-level middleware installed BEFORE
// the engine's contextInit.
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
//	h, _ := svckit.New(ctx, cfg,
//	    svckit.WithCORS("https://app.example.com", "https://admin.example.com"))
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
// Calling WithCORS / WithCORSConfig more than once keeps only the
// last config (last-write-wins).
func WithCORSConfig(cfg cors.Config) Option {
	return func(o *options) {
		o.corsHandler = cors.New(cfg)
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

// WithoutSecurityHeaders suppresses the auto-installed OWASP security
// headers middleware (HSTS, X-Content-Type-Options, X-Frame-Options,
// Referrer-Policy, CSP). Use when the headers are handled upstream
// (CDN, reverse proxy) or when the service is internal-only and the
// operator has decided the cost of the extra headers isn't worth
// paying.
func WithoutSecurityHeaders() Option {
	return func(o *options) { o.skipSecurityHeaders = true }
}

// WithSecurityHeaders configures the auto-installed OWASP security
// headers middleware. Forwards any [fibermap.SecurityHeadersOption]
// — e.g. [fibermap.WithHSTSIncludeSubdomains], [fibermap.WithCSP],
// [fibermap.WithoutHSTS]. The middleware is installed regardless;
// pass [WithoutSecurityHeaders] instead to suppress it entirely.
func WithSecurityHeaders(opts ...fibermap.SecurityHeadersOption) Option {
	return func(o *options) { o.securityHeaderOpts = append(o.securityHeaderOpts, opts...) }
}

// WithoutBearerOptionalLayer skips installing auth.Bearer(BearerOptional)
// at the fiber.App level. Only sensible if you have no auth or want to
// orchestrate the layer yourself.
func WithoutBearerOptionalLayer() Option {
	return func(o *options) { o.skipBearerLayer = true }
}

// WithoutLoggerInjector suppresses the auto-installed
// [fibermap.LoggerInjector] middleware. By default New installs it at
// the App level so handlers can call `fibermap.LoggerFrom(c)` and get
// a *slog.Logger pre-bound with `method`, `path`, `request_id`,
// `user_id` (when authenticated), and `route` (when set).
//
// Disable when:
//   - You install your own request-scoped logger middleware.
//   - You don't care about the per-request enrichment and want to
//     keep the middleware chain minimal.
func WithoutLoggerInjector() Option {
	return func(o *options) { o.skipLoggerInjector = true }
}

// WithBodyLimit overrides Fiber's default request-body limit
// (4 MiB) with the supplied byte count. Fiber returns 413
// Request Entity Too Large when an inbound request exceeds the
// limit — set this tight when the service only accepts small
// JSON payloads to blunt accidental or malicious oversize POSTs.
//
//	svckit.WithBodyLimit(64 * 1024) // 64 KiB cap
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
// installed unconditionally regardless of [WithBodyLimit].
//
// Pass a wrapper to layer cross-cutting behaviour on top of the kit
// default — the typical case is sentrykit 5xx auto-capture:
//
//	svc, _ := svckit.New[AppCtx, Claims](ctx, cfg,
//	    svckit.WithErrorHandler(
//	        sentrykit.WrapErrorHandler(fibermap.ErrorHandler(logger))))
//
// `sentrykit.WrapErrorHandler` reports the error to the per-request
// Sentry hub BEFORE delegating to the supplied fibermap.ErrorHandler
// for the actual HTTP response. Without this option, the kit's
// auto-installed default ErrorHandler runs directly — *errs.Error
// still maps to the right JSON shape, but Sentry sees nothing.
//
// Pass nil to keep the kit default (equivalent to never calling
// WithErrorHandler at all). Caller-supplied [fibermap.WithFiberConfig]
// via [WithRunOptions] still wins over this option (it's applied
// later in the RunOption chain) — use either WithErrorHandler OR a
// custom fiber.Config, not both.
func WithErrorHandler(h fiber.ErrorHandler) Option {
	return func(o *options) { o.errorHandler = h }
}

// WithHTTPCOptions appends to the httpc options applied by New
// (logger + metrics are already auto-applied).
func WithHTTPCOptions(opts ...httpc.Option) Option {
	return func(o *options) { o.httpcOpts = append(o.httpcOpts, opts...) }
}

// WithDBOptions appends to the db options applied by New. `db.WithLogger`
// and `db.WithMetrics` are already wired automatically (the latter
// when [WithMetrics] is also configured — see [WithoutAutoDBMetrics]
// for the opt-out). Use this for `db.WithSlowQueryThreshold`,
// additional `db.WithTracer` calls, `db.WithReadLagBudget`,
// `db.WithReplicaLagPolling`, or any future db option the kit grows.
//
// DO NOT pass `db.WithMetrics(reg)` here unless you ALSO pass
// [WithoutAutoDBMetrics] — the kit's auto-wiring + this duplicate
// registers the same collectors twice on the same registry and
// `prometheus.MustRegister` panics.
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
func WithoutAutoDBMetrics() Option {
	return func(o *options) { o.skipAutoDBMetrics = true }
}

// WithoutConnectRetry disables the auto-injected K8s-friendly retry
// defaults for DB Connect calls. Use when the deployment strictly
// orders dependencies (e.g. init-containers) and prefers fast-fail
// diagnostics over patience.
func WithoutConnectRetry() Option {
	return func(o *options) { o.skipConnectRetry = true }
}

// WithRoutes enables routes auto-load in Run using DefaultRoutesPath.
// Equivalent to setting Config.Routes.Enabled = true. Missing file at
// Run time produces CodeRoutesYAMLNotFound.
func WithRoutes() Option {
	return func(o *options) { o.routesEnable = true }
}

// WithRunOptions appends fibermap.RunOption entries to the default
// production-ops bundle Run uses.
func WithRunOptions(opts ...fibermap.RunOption) Option {
	return func(o *options) { o.runOpts = append(o.runOpts, opts...) }
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
func WithOpenAPI(opts ...openapi.Option) Option {
	return func(o *options) {
		o.openapiEnable = true
		o.openapiOpts = append(o.openapiOpts, opts...)
	}
}

// WithoutReadiness suppresses the auto-installed /readyz endpoint.
// By default New auto-mounts /readyz, which pings every checker in
// [Service.readinessCheckers] (DB plus whatever mods or
// [WithReadinessChecker] added) in parallel — pass this option when
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
// receives this ctx, so a slow checker doesn't block the others
// indefinitely. 0 → fibermap's built-in default (5s).
func WithReadinessTimeout(d time.Duration) Option {
	return func(o *options) { o.readinessTimeout = d }
}

// WithReadinessChecker appends app-level checkers to the auto-wired
// subsystem set. Each checker must satisfy `fibermap.Checker` —
// `Name() string` + `Check(ctx) error`. Use for migration probes,
// cache warmup gates, external API pings the service must clear
// before serving traffic.
//
// Order: this option writes to the same accumulator mods append to
// via Host.AddReadinessChecker during their Build phase. New applies
// every Option func — including WithReadinessChecker — in one pass
// before any mod's Build phase runs, so checkers passed here always
// land BEFORE mod-contributed ones in Service.readinessCheckers(),
// regardless of where WithReadinessChecker sits relative to WithMod
// in the New(...) argument list. Readiness / preflight JSON is read
// top-to-bottom as a dependency tree, so keep that ordering in mind
// when it matters to an operator.
func WithReadinessChecker(c ...fibermap.Checker) Option {
	return func(o *options) { o.readinessCheckers = append(o.readinessCheckers, c...) }
}

// WithPreflightEndpoint mounts the `/preflight` HTTP endpoint that
// renders [PreflightResult] as JSON (200 on success, 503 on any
// failure). Useful for ops smoke-tests and CI pipelines that need an
// "is staging actually ready" gate before running integration tests.
//
// Path is configurable; pass "" to use the default "/preflight".
//
// Unlike `/readyz` (auto-mounted, K8s readiness probe), preflight is
// opt-in — it's a debug / ops surface, not part of the request path.
// Don't wire it as a K8s readinessProbe unless you've raised the
// probe timeout to accommodate slower checks.
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

// WithRefreshGC schedules periodic garbage collection of expired
// refresh tokens against the refresh store wired through Auth.
// Without it, the underlying table grows forever — even though
// expired entries no longer authenticate anything, they cost storage
// and slow Consume's diagnostic SELECT.
//
//	svckit.WithRefreshGC(15 * time.Minute)
//
// interval <= 0 disables the feature (same as not calling the
// option). Auth must be configured for the GC to start; otherwise the
// option is a no-op.
func WithRefreshGC(interval time.Duration) Option {
	return func(o *options) { o.refreshGCInterval = interval }
}

// WithMigrations applies the migrations bundled into fsys via
// [migrate.Up] right after the DB pool is built and before any
// subsystem that reads schema. Files use the `NNNN_name.sql`
// convention; see the db/migrate README for details on the
// schema_migrations tracking table and the optional
// `-- @migrate:no-transaction` directive.
//
//	//go:embed migrations/*.sql
//	var migrationsFS embed.FS
//
//	h, _ := svckit.New(ctx, cfg, svckit.WithMigrations(migrationsFS))
//
// Migration failures surface from New with the original *errs.Error
// from db/migrate (CodeApplyFailed, CodeBootstrapFailed, etc) — no
// extra wrapping.
//
// Off by default — operators who run a separate migration tool
// (golang-migrate, goose) skip this option and apply schema before
// process start.
func WithMigrations(fsys fs.FS) Option {
	return func(o *options) { o.migrationsFS = fsys }
}

// WithDBDrainTimeout caps the wait for in-flight DB queries /
// transactions during shutdown. Default 5s — accommodates a burst of
// normal handlers without holding the SIGTERM-deadline hostage to a
// stuck query.
//
// Set to 0 to keep the default; a positive value overrides it.
func WithDBDrainTimeout(d time.Duration) Option {
	return func(o *options) { o.dbDrainTimeout = d }
}

// WithTLS makes Run serve HTTPS: fibermap's Run switches from
// app.Listen to app.ListenTLS(addr, certFile, keyFile). Use for edge
// deployments where the service faces the network itself (no
// ingress/nginx terminating TLS) and for locally exercising
// Secure-cookie flows. For ingress-terminated deployments keep plain
// HTTP — that split is by design.
//
// Both files must be PEM-encoded. The pair is all-or-nothing:
// supplying only one makes Run fail with fibermap's
// *Error{Code: invalid_tls_config} instead of silently starting plain
// HTTP. The env equivalent is TLS_CERT_FILE / TLS_KEY_FILE in
// [ServiceConfig] (validated at Config.Validate); this option wins
// over the env pair when both are present.
func WithTLS(certFile, keyFile string) Option {
	return func(o *options) {
		o.tlsCert = certFile
		o.tlsKey = keyFile
	}
}
