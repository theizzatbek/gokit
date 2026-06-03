# Changelog

All notable changes to fibermap. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versioning is
0.x — every minor bump may include breaking changes until a 1.0 tag.

This is the bootstrap entry; prior history lives in `git log`.

## [Unreleased]

### Added
- `clients/natsmap/` — five additions that open up the natsclient
  handler-resilience pack to natsmap users + add hooks, metrics,
  default-headers, and mock mode for unit-testing without NATS.
  - `WithSubscribeOptions(...natsclient.SubscribeOption)` engine-wide
    + `Engine.RegisterSubscriberOptions(name, ...natsclient.SubscribeOption)`
    per-subscriber. Per-subscriber opts are appended AFTER the global
    slice at Build. Unknown subscriber names fail Build with
    `natsmap_unknown_subscriber`.
  - `WithBeforeDispatch(func(name, subject))` /
    `WithAfterDispatch(func(name, subject, err, elapsed))` —
    subscriber-side hooks visible from the YAML-declared name.
    Wrapped around the user handler before SubscribeRaw so the
    callbacks fire in-band; metrics observation rides the same
    wrapper for outcome classification.
  - `WithBeforePublish(func(ctx, name, subject, headers))` /
    `WithAfterPublish(func(ctx, name, subject, err, elapsed))` —
    publisher-side hooks. beforePublish gets the merged final
    headers map and mutations land on the wire.
  - `WithDefaultPublishHeaders(map[string][]string)` engine-wide
    defaults merged into every Publish / PublishRaw. Layering:
    defaults → YAML publisher static → per-call (last wins on
    per-key conflict). X-Request-ID from ctx still auto-injects.
  - `WithMetrics(reg)` now wires natsmap-owned collectors:
    `natsmap_handlers_total{name,outcome}`,
    `natsmap_handler_duration_seconds{name}`,
    `natsmap_publishes_total{name,outcome}`. Cardinality bounded by
    YAML-declared name; subscription-level series stay on
    clients/nats.
  - `RegisterMockHandler[T](e, name, fn)` + `DispatchMock[T](ctx,
    runtime, name, payload, headers)`. Mock subscribers skip every
    NATS-side wiring at Build; DispatchMock fires the registered fn
    synchronously on the caller's goroutine. Production must NOT
    call DispatchMock. Build now also tolerates a nil
    *natsclient.Client when every subscriber is a mock and no
    publisher is declared; publishers in that mode install
    error-stubs so accidental Publish calls surface loud.
- `clients/apimap/` — four additions that open up the new httpc
  features to apimap users + add mock-mode and default-Call layering.
  - `WithHTTPCOptions(...httpc.Option)` engine-wide passthrough +
    `Engine.RegisterClientOptions(clientName, ...httpc.Option)`
    per-client. Per-client opts are appended AFTER the global slice
    at Build so client-specific options refine rather than replace
    the global baseline. Unknown client names fail Build with
    `apimap_unknown_client`.
  - `WithBeforeRequest(func(client, endpoint, *http.Request))` /
    `WithAfterResponse(func(client, endpoint, req, resp, err,
    elapsed))` — apimap-level lifecycle hooks. Implemented as an
    httpc middleware that reads the endpoint name from a private
    context key, so the callbacks see the kit-stable (client,
    endpoint) pair even when a single *http.Client is shared across
    endpoints.
  - `WithDefaultCall(Call)` engine-wide +
    `Engine.SetClientDefaultCall(clientName, Call)` per-client.
    Defaults are merged before the caller's Call (engine → client →
    caller, last wins on per-key conflict). Containers (Path / Query
    / Headers) merge by key; URL/Body take the last non-zero value.
    `mergeCalls` helper exposed inside the package.
  - `Engine.RegisterTransport(clientName, http.RoundTripper)` — mock
    mode. Replaces the per-client base transport at Build with the
    supplied RoundTripper; the breaker / bulkhead / retry chain still
    wraps it so the mock path goes through observability. Unknown
    names fail Build with `apimap_unknown_client`.
- `clients/httpc/` — retry-policy customization, middleware chain,
  transport shortcuts, lifecycle hooks.
  - `WithRetryClassifier(func(*http.Request, *http.Response, error) bool)`
    overrides the kit's default decision rule. Honoured for BOTH the
    network-error path and the status path — a custom classifier can
    veto a transient network failure (e.g. don't retry
    context.Canceled-shaped errors that bubble through a third-party
    transport).
  - `WithRetryStatusCodes(...int)` atomically replaces the
    transient-status set (default `408, 429, 500, 502, 503, 504`).
    Useful when the caller handles 429 with its own rate-limit
    replay.
  - `IsDefaultRetryableStatus(int) bool` exposes the default status
    set as a building block for `WithRetryClassifier` (add to the
    default rather than replacing it).
  - `WithRetryOnNonIdempotent(bool)` and
    `WithIdempotencyKeyHeader(name string)` unlock POST/PATCH retry.
    The header form is Stripe-style — retry happens only when the
    outbound request carries the named header.
  - `WithMiddleware(...func(http.RoundTripper) http.RoundTripper)`
    appends RoundTripper decorators layered ABOVE retry+metrics,
    BELOW the X-Request-ID stamp. Applied in reverse so the first
    middleware is the outermost (matches stdlib middleware
    conventions).
  - `WithBeforeRequest(func(*http.Request))` /
    `WithAfterResponse(func(req, resp, err, elapsed))` are short-API
    hooks over `WithMiddleware` for header stamping / audit logging.
    Multiple calls — last wins.
  - `WithProxy(*url.URL)` / `WithDialer(...)` / `WithTLSConfig(*tls.Config)`
    populate a shared `*http.Transport`. Compose into one Transport
    via repeated calls instead of each replacing the previous.
    Explicit `WithBaseTransport` with a non-`*http.Transport` (otel
    wrapper) wins — shortcuts no-op silently.
- `clients/nats/` — five additions covering handler resilience, sync
  consumption, and federation.
  - `WithErrorClassifier(func(error) AckAction)` — declarative
    routing of handler errors to Ack / Nak / Term. Default keeps the
    legacy contract (nil → Ack, ErrPoison → Term, anything else →
    Nak). Lets validation errors Term while transient errors Nak.
  - Panic recovery inside `dispatchRaw` — the goroutine slot is
    released regardless of what the handler does; the panic becomes a
    Nak with a Warn-log; `WithPanicHandler(func(any))` is the optional
    app-side callback (Sentry capture, custom counter).
  - `WithAckProgress(d)` auto-heartbeat — kit fires `InProgress()`
    every `d` while the handler runs so long-running work survives
    AckWait without manual heartbeats. `Msg[T].InProgress()` /
    `RawMsg.InProgress()` are the manual escape hatch.
  - `NewPullSubscription[T]` + `(*PullSubscription).Fetch` / `.Run` /
    `.Drain` — typed pull-mode consumer for cron-style /
    backpressure-sensitive workers. Decoded into `PendingMsg[T]` with
    explicit Ack / Nak / Term. Decode failures auto-Term'd as
    poison-pills inside Fetch; successful decodes still come through.
  - `WithTLSConfig` / `WithRootCAs` / `WithClientCert` — TLS material
    for public-internet NATS. WithTLSConfig is verbatim; WithRootCAs +
    WithClientCert compose piecewise (mutually exclusive with
    WithTLSConfig). Partial WithClientCert wiring is caught at
    Connect.
  - `Request[Req, Resp]` / `Reply[Req, Resp]` — typed RPC primitives
    over `conn.RequestMsgWithContext`. Both sides go through the
    client codec; trace context is propagated. New `Code*` constants
    `request_timeout` / `request_failed`.
  - `EnsureKVBucket(ctx, KVConfig) → nats.KeyValue` +
    `NewKV[T](c, bucket) *KV[T]` — typed handle over JetStream KV.
    Get / Put / Update (CAS via revision) / Delete / Raw().
    `kv_key_not_found` (NotFound) and `kv_op_failed` (Conflict for
    Update, Unavailable for other ops) are stable codes.
- `auth/` — six additions covering federation, operator UX, and SecOps.
  - `Auth.JWKSHandler(maxAge int)` + `KeySet.JWKS() ([]byte, error)`
    render the verify set as RFC 7517 JWKS. EdDSA → `kty=OKP/crv=Ed25519/x`,
    ES256 → `kty=EC/crv=P-256/x,y`. `Auth.KeySet()` exposes the live set
    via atomic load so callers can serve it themselves.
  - `Auth.RotateKeys(*KeySet) error` hot-swaps signing material under
    concurrent Sign/Verify (atomic.Pointer; no lock). Validates the
    incoming set (non-nil, non-empty verify, active key has private
    material when active.KID is set). Verify automatically accepts every
    alg present in the new set — mixed EdDSA + ES256 rotation works.
  - `Auth.RequireAnyScope(...) / RequireAnyRole(...)` — OR-semantic
    counterparts to existing AND-form. YAML factories
    `require_any_scope` / `require_any_role` registered through
    `auth/fibermount.MountMiddlewareFactories`.
  - `RevokedAccessStore` interface + `MemRevokedAccessStore` default +
    `WithRevokedAccessStore` option. Bearer middleware consults the
    blacklist after JWT verify, fail-OPEN on backend error
    (transient outage doesn't lock out every user). `Auth.RevokeAccess(ctx,
    Claims[C])` is the admin-side write path. Stable code:
    `token_revoked` (401).
  - `KeyUsageTracker` optional contract — `KeyStore` implementations
    MAY satisfy `MarkUsed(ctx, id, t)` to record per-key last-used
    timestamps. APIKey middleware type-asserts once and fires
    `MarkUsed` in a background goroutine (5s ctx) so the hot path
    stays allocation-free.
  - `WithIPExtractor(IPExtractor)` overrides `c.IP()` for the whole
    Auth bundle — refresh-token meta, security log, rate-limit
    fallback bucket all route through `Auth.clientIP`. Empty return
    falls back to fiber's stdlib `c.IP()`. `Auth.RateLimit` /
    `RateLimitBySubject` now use Auth-bound keyers so CDN-aware IP
    extraction reaches the limiter buckets too.
- `db/` — five production-oriented helpers around the existing pgx wrapper.
  - `db.(*DB).TxRetry(ctx, fn, opts...)` — auto-retry on SQLSTATE
    `40001` (serialization failure) and `40P01` (deadlock detected)
    with exponential backoff + ±25% jitter. Defaults:
    `MaxAttempts=3`, `BaseBackoff=5ms`, `MaxBackoff=100ms`. Options:
    `WithTxRetryMaxAttempts`, `WithTxRetryBackoff`,
    `WithTxRetryClassifier`, `WithTxRetryOpts(TxOpts)`. Helper
    `db.IsRetryableTxConflict(err)` walks the error chain via
    `errors.As` so wrapped `*errs.Error` still classifies. New
    counter `db_tx_retries_total` increments once per retry attempt
    (terminal outcomes stay in `db_tx_total{kind=tx,outcome=…}`).
  - `db.(*DB).TxWithOpts(ctx, TxOpts, fn)` + kit-stable `IsoLevel` /
    `TxAccessMode` / `TxDeferrableMode` constants. `Tx` becomes a
    thin shortcut for `TxOpts{}`. Pair `TxOpts{IsoLevel:
    Serializable}` with `TxRetry` for the canonical strict-isolation
    pattern.
  - `db.WithDefaultStatementTimeout(d)` — sets server-side
    `statement_timeout` via an `AfterConnect` hook so a runaway
    query is killed on the server even when the caller's
    `context.WithTimeout` only kills the local goroutine.
  - `db.WithConnInit(fn ConnInitFn)` — generic per-connection hook
    chained after the statement-timeout setter. Multiple calls
    accumulate in registration order; used for `SET
    application_name`, `SET search_path`, `SET ROLE`, or
    prepared-statement warming.
  - `db.(*DB).HealthcheckRead(ctx)` — pings the read-replica pool
    when `HasReadReplica=true`; returns nil when no standby
    configured. Surfaces silent standby loss that `ReadQuery`'s
    primary-fallback hides.
  - `db.(*DB).CopyFrom` / `db.(*Tx).CopyFrom` — thin wrappers over
    pgx's COPY protocol with the same `mapPgxErr` funnel as
    `Query`/`Exec`.
- `clients/webhooks/` — outbound + inbound HTTP webhooks subsystem.
  - Core: `Subscription` + `Delivery` types, `SubscriptionStore` /
    `DeliveryStore` interfaces, `Signer` (Stripe-style HMAC),
    `Verifier` interface, `Fanout` (event → N deliveries, idempotent
    via UNIQUE constraint), `Worker` (per-target retry/backoff/DLQ),
    `RetentionWorker` (TTL-driven sweep of delivered rows).
  - `clients/webhooks/storepg` — Postgres backend with AES-256-GCM
    secret-at-rest (key via `WEBHOOKS_SECRET_KEY`, 32 bytes base64;
    version-prefixed ciphertext).
  - `clients/webhooks/verifiers` — `GenericHMAC` (configurable
    scheme, optional timestamp window) + `GitHub` preset.
  - `fibermap/webhookguard` — Fiber middleware that verifies the
    inbound signature via any `webhooks.Verifier` and returns 401
    via the kit's `errs.HTTP` mapping on mismatch.
  - `service.WithWebhooks` — wires `Worker` into the lifecycle and
    drains it via `OnShutdown` before NATS/DB teardown;
    `Service.WebhooksFanout` is exposed for the caller to register
    inside their `WithNATSMapRegistration` handler.

## [v0.8.2] - 2026-05-23

Finishes the OpenAPI-schema fix that v0.8.1 only partially shipped.

### Fixed
- `examples/tasks/main.go` still passed `fiber.Map{...}` literals to
  `openapi.WithDefaultResponse` (for the universal 4xx/5xx) and to
  `fibermap.WithResponse(fiber.StatusOK, …)` on `tasks.list`. v0.8.1
  introduced the typed `tasks.ListResponse` / `tasks.ErrorResponse`
  structs in the internal handler code but never wired them into the
  generator options — Scalar / Swagger UI continued to show empty
  response bodies. v0.8.2 actually swaps those call sites over.

## [v0.8.1] - 2026-05-23

Documentation fix for the example. No library API changes — only
`examples/tasks` got typed response wrappers so the generated
OpenAPI spec advertises proper schemas.

### Fixed
- `examples/tasks` was using `fiber.Map{...}` for the list-response
  and error-response shapes in `WithResponse` / `WithDefaultResponse`
  calls. `fiber.Map` is `map[string]any`, so invopop/jsonschema
  produced an opaque `{type: object}` schema with no field info —
  Swagger UI / Scalar showed empty bodies. Replaced with typed
  structs:
  - new `tasks.ListResponse{Tasks []Task}` for `GET /tasks`
  - new `tasks.ErrorResponse{Error string}` for every 4xx/5xx
  - `Handler.List` returns `ListResponse{...}` instead of
    `fiber.Map{"tasks": ...}`
  - the internal `notFound` / `badRequest` helpers and the 500
    fallback all now emit `ErrorResponse`
  - `main.go` uses `tasks.ListResponse{}` and
    `tasks.ErrorResponse{}` in the generator options
  Pattern guidance for users: any type you advertise via
  `WithResponse` / `WithDefaultResponse` (or `WithBody` /
  `WithQuery` etc) should be a concrete struct, not an
  un-typed map.

## [v0.8.0] - 2026-05-23

Removes the last bit of binding boilerplate. Handlers can now take a
typed request body as a second parameter; fibermap parses + validates
+ feeds it in before invocation. The body schema is auto-attached for
OpenAPI — the request type appears once at the call site instead of
three times (handler signature + bind.Body call + WithBody option).

### Added
- `fibermap.RegisterHandler(eng, name, h, opts...)`,
  `fibermap.RegisterMiddleware(eng, name, m)`, and
  `fibermap.RegisterMiddlewareFactory(eng, name, f)` —
  package-level forms of the existing `Engine.Register*` methods.
  Exist so callers can use a uniform `fibermap.Register*(eng, ...)`
  style throughout — including with the new typed-body helpers
  below, which CAN'T be methods because Go disallows generic
  methods. The original `Engine.Register*` methods remain; both
  forms are exported and equivalent.
- `fibermap.RegisterHandlerWithBody[T, Req any](eng, name, h, opts...)` — wraps
  a typed handler `func(*Context[T], Req) error` into a
  `HandlerFunc[T]` that parses the body via `bind.Body[Req]`, runs
  the engine's validator, and invokes `h` with the result. Schema is
  auto-attached via `WithBody(*new(Req))`.
- `fibermap.RegisterHandlerWithQuery` / `RegisterHandlerWithParams` / `RegisterHandlerWithHeaders` —
  same shape for the other bind locations, reading from the
  `query:` / `params:` / `reqHeader:` struct tags. Auto-attach the
  matching schema where applicable.
- `Engine.SetValidator(v bind.Validator)` — engine-wide validator
  used by the Register* helpers. nil disables validation (matches
  the bind helpers' nil-validator behaviour).
- `Engine.SetBindErrorHandler(fn BindErrorFunc[T])` — customizes the
  response when a Register*-wrapped handler hits a parse / validate
  error. Default returns 400 with `{"error": err.Error()}`. Inspect
  the error with `errors.Is(err, bind.ErrParseBody)` etc to branch.
- `fibermap.BindErrorFunc[T]` — public type for the bind error
  handler signature.
- `examples/tasks` migrates `tasks.create` and `tasks.update` to
  `fibermap.RegisterHandlerWithBody` — handler signatures gain a typed `req`
  parameter and the per-handler `bind.Body` + `badBody` branching
  disappears. Net ~15 lines fewer in the example.

## [v0.7.0] - 2026-05-23

OpenAPI ergonomics pass:
  - schemas now attach at `RegisterHandler` time (removes the
    `OnHandler` builder and the duplicated handler name);
  - `Generator.Mount()` wires `/openapi.json` + `/docs` in one call
    (replaces ~25 lines of `Engine.Add` + `sync.Once` boilerplate);
  - three HTML viewer helpers (Swagger UI / Redoc / Scalar);
  - `openapi.SecurityMapping(...)` combines `WithSecurity` +
    `MapMiddlewareToSecurity` into one call (these were always used
    together).

### Changed
- `Engine.RegisterHandler` is now variadic: it accepts optional
  [HandlerOption] values that attach typed request/response schemas
  to the handler. Backwards-compatible — existing calls that pass
  only the handler still work.
- `openapi.Generator.OnHandler` + `HandlerSchemaBuilder` removed.
  Schemas live on the engine via `fibermap.With{Body,Query,Headers,Response}`.
  The generator reads them via `Engine.HandlerMeta(name)`.

### Added
- `fibermap.HandlerOption` + `WithBody(model)` / `WithQuery(model)`
  / `WithHeaders(model)` / `WithResponse(status, model)` —
  introspection-only metadata attached at `RegisterHandler` time.
  Runtime ignores them; `fibermap/openapi` reads them for spec
  generation.
- `Engine.HandlerMeta(name) *HandlerMeta` getter — opens the same
  metadata to any introspection tool (not just openapi).
- `openapi.SecurityMapping(name, scheme, middlewares...)` —
  one-call combination of `WithSecurity` + one-or-more
  `MapMiddlewareToSecurity`. The originals stay for callers that
  need finer control.
- `Generator.Mount(opts ...MountOpts) error` — installs two
  programmatic routes on the generator's engine:
    - `GET /openapi.json` — spec, generated lazily on first request,
      cached for subsequent requests.
    - `GET /docs` — HTML viewer pointing at the spec.
  Both paths and the docs viewer are overridable via `MountOpts`
  (zero value uses sensible defaults — Scalar viewer, the
  generator's Info.Title as the docs title).
- `openapi.MountOpts` — config struct for `Mount` with `SpecPath`,
  `DocsPath`, `DocsTitle`, `DocsViewer` fields.
- `openapi.SwaggerUI(specURL, title)` — Swagger UI 5.x via unpkg.
- `openapi.Redoc(specURL, title)` — Redoc 2.x via unpkg.
- `openapi.Scalar(specURL, title)` — Scalar API Reference via
  jsdelivr (modern UI, dark-mode default, built-in API client).
  Default viewer for `Mount`.
- All three viewer helpers return a self-contained HTML string with
  user input HTML-escaped (`html.EscapeString`) — safe to pass
  title/url from config or environment.
- `examples/tasks/main.go` migrates to the new pattern: schemas at
  `RegisterHandler`, single `gen.Mount()` for spec + docs,
  `SecurityMapping` instead of two separate options.

### Changed (continued)
- `openapi.MapMiddlewareToSecurity` and `openapi.SecurityMapping`
  now accumulate when the same middleware is mapped to multiple
  schemes — both schemes appear in the operation's `security` array
  as separate entries (OR semantics: any one satisfies). This lets
  an `auth` middleware that accepts both Bearer and Basic
  credentials advertise both in the spec.
- `examples/tasks/internal/auth`: added `Basic()` middleware (HTTP
  Basic against an in-memory user table) and `BearerOrBasic()` that
  dispatches on the `Authorization` header prefix. The example wires
  `BearerOrBasic()` and registers both `BearerAuth` + `BasicAuth`
  schemes in the OpenAPI spec.
- `examples/tasks/main.go`: dropped the hardcoded
  `WithServer("http://localhost:3000", ...)` — without it, OpenAPI
  tools resolve URLs relative to where the spec is served
  (correct for both local and production). A comment shows how to
  wire `os.Getenv("API_BASE_URL")` for prod.

### Notes
- v0.6.0's `openapi.OnHandler` API was never tagged as stable
  (0.x). Callers that used it should migrate to
  `fibermap.WithBody`/`WithResponse` at `RegisterHandler`.

### Added
- `Generator.Mount(opts ...MountOpts) error` — installs two
  programmatic routes on the generator's engine:
    - `GET /openapi.json` — spec, generated lazily on first request,
      cached for subsequent requests.
    - `GET /docs` — HTML viewer pointing at the spec.
  Both paths and the docs viewer are overridable via `MountOpts`
  (zero value uses sensible defaults — Scalar viewer, the
  generator's Info.Title as the docs title).
- `openapi.MountOpts` — config struct for `Mount` with `SpecPath`,
  `DocsPath`, `DocsTitle`, `DocsViewer` fields.
- `openapi.SwaggerUI(specURL, title)` — Swagger UI 5.x via unpkg.
- `openapi.Redoc(specURL, title)` — Redoc 2.x via unpkg.
- `openapi.Scalar(specURL, title)` — Scalar API Reference via
  jsdelivr (modern UI, dark-mode default, built-in API client).
  Default viewer for `Mount`.
- All three viewer helpers return a self-contained HTML string with
  user input HTML-escaped (`html.EscapeString`) — safe to pass
  title/url from config or environment.
- `examples/tasks/main.go` now calls `gen.Mount()` instead of the
  hand-rolled spec + docs wiring it had in v0.6.0.

## [v0.6.0] - 2026-05-23

OpenAPI 3.0 spec generation. Most users of declarative routers pick
them because of this feature — fibermap now ships it as a subpackage
that reads the engine's introspection API and emits a valid OpenAPI
document.

### Added
- New subpackage `fibermap/openapi`:
  - `Generator[T]` with fluent `OnHandler(name).Body / .Query /
    .Headers / .Response / .Summary / .Description` builder.
  - Options: `WithInfo`, `WithServer`, `WithSecurity`,
    `MapMiddlewareToSecurity`.
  - Security helpers: `HTTPBearer`, `HTTPBasic`, `APIKey`.
  - `Generate() ([]byte, error)` returns JSON-formatted OpenAPI 3.0.3.
  - Auto-translation of Fiber path params (`:id` → `{id}`),
    auto-declaration of path parameters, security mapped from the
    middleware chain on each route.
  - Schema reflection via
    [`invopop/jsonschema`](https://github.com/invopop/jsonschema) —
    honours `json:`, `validate:`, and `description:` struct tags.
    Referenced types live under `components.schemas` and are
    referenced via `$ref`.
- `examples/tasks` serves the generated spec at `/openapi.json` via
  `Engine.Add` + `sync.Once` cache; demonstrates typed Body /
  Response declarations for create / update / get / list / delete.

### Changed
- New optional `summary:` YAML field on routes — short one-line
  title, surfaced via `RouteInfo.Summary` and mapped to
  `operation.summary` in the generated OpenAPI document. The longer
  `description:` field continues to map to `operation.description`.
- `examples/tasks/internal/tasks`: the per-request body types
  `createReq` and `updateReq` are renamed to exported `CreateReq` /
  `UpdateReq` so external code (`main.go`'s OpenAPI wiring) can
  reference them. No behaviour change.
- The OpenAPI builder no longer exposes `Summary` / `Description`
  setters — operation text lives in `routes.yaml` (the `summary:`
  and `description:` fields). The builder is reserved for typed
  Go schemas.

### Deps
- Adds `github.com/invopop/jsonschema` (and its transitive deps:
  `pb33f/ordered-map`, `go.yaml.in/yaml/v4`) to the dependency graph.

## [v0.5.0] - 2026-05-23

A "smart defaults" release: most of the v0.3 ops bundle is now ON BY
DEFAULT inside `Engine.Run`. The intent is "your service should be
observable, recoverable, and probe-able with zero opt-in code".

### Changed
- `Engine.Run` now installs the following automatically when the
  caller didn't pass an opt-out:
  - **Recover** with `slog.Default()` — panics in any downstream
    handler/middleware are logged with stack + 500 returned.
  - **RequestID** prepended to the Use chain — every response carries
    a 16-hex `X-Request-ID`.
  - **RequestLogger** with `slog.Default()`, skipping `/healthz` and
    `/metrics` — one structured access-log line per request.
  - **HealthCheck** at `/healthz` — 200 OK, bypasses every middleware.
  These defaults apply to **all** engines — both `New[T]()` and
  `Default[T]()`. **This is a behavior change for existing callers
  that already serve `/healthz` themselves or that intentionally
  wanted zero middleware** — use the new `Without*` options listed
  below to opt out.
- `fibermap.Default[T]()` simplified: previously a "full ops bundle"
  shortcut, now equivalent to `New[T]()` plus `WithMetrics("/metrics")`.
  Since the rest of the bundle moved into `Run` itself, the only
  thing Default still adds is the Prometheus endpoint (which stays
  opt-in because it pulls in `prometheus/client_golang`).
- `examples/tasks/main.go` switched to `Default[T]()` and dropped the
  explicit `WithRecover` / `WithHealthCheck` / `WithMetrics` /
  `WithUse(RequestID(), ...)` calls. Only `WithRequestLogger(logger,
  ...)` is kept (custom slog logger instead of `slog.Default()`).

### Added
- `WithoutRecover()` — opt out of the built-in Recover default.
- `WithoutRequestID()` — opt out of the built-in `RequestID` default.
- `WithoutRequestLogger()` — opt out of the built-in access logger.
- `WithoutHealthCheck()` — opt out of the built-in `/healthz` route
  (equivalent to `WithHealthCheck("")`).
- `WithoutMetrics()` — opt out of `WithMetrics` (useful when an
  engine constructed via `Default[T]()` doesn't want the
  Prometheus endpoint after all).

## [v0.4.1] - 2026-05-23

Maintenance release. No API surface changes, no behaviour changes —
the two improvements are observable only via benchmarks (`Lookup`)
and idiom (`All`).

### Added
- `Engine.All() iter.Seq[RouteInfo]` — Go 1.23+ range-over-func
  iterator over the route table. Idiomatic alternative to `Walk`
  (which stays for callers that need the error-propagating callback
  shape):

  ```go
  for r := range eng.All() {
      if strings.HasPrefix(r.Path, "/internal/") { continue }
      fmt.Println(r.Method, r.Path)
  }
  ```

### Changed
- `Engine.Lookup(method, path)` is now O(1). A
  `map[method+space+path] → index` is built at Mount alongside the
  route slice; `Lookup` consults the map instead of linearly scanning.
  Benchmarks: ~30ns / 0 allocs regardless of route count
  (previously linear). `Walk` and `Routes` still iterate the slice
  in insertion order.

## [v0.4.0] - 2026-05-23

A "dev velocity + API gaps" release. Closes three common pain points:
binding request headers (the missing `bind.Header[T]`), defining
routes that don't fit YAML (`Engine.Add`), and the boilerplate of
wiring the v0.3 ops bundle every time (`fibermap.Default[T]`).

### Added
- `fibermap.Default[T]()` — constructor that returns an Engine
  pre-wired with the v0.3 ops bundle (`Recover` + `RequestID` +
  `RequestLogger` + `HealthCheck` + `Metrics`). `eng.Run()` ships
  with sensible production defaults. Defaults are applied BEFORE
  user options, so any explicit option (`WithMetrics("")`,
  `WithHealthCheck("/_health")`, `WithRecover(myLogger)`) overrides
  the default.
- `bind.Header[T]` + `ErrParseHeader` / `ErrValidateHeader` — fourth
  in the bind family alongside `Body` / `Query` / `Params`. Wraps
  Fiber's `ReqHeaderParser`; struct fields use the `reqHeader:`
  tag. Typical targets: `Authorization`, `X-Idempotency-Key`,
  `Accept-Language`.
- `Engine.Add(method, path, name, handler, [AddOpts{...}])` —
  programmatic route registration for handlers that don't fit the
  YAML model (debug/pprof, dynamic admin routes, embedded UIs).
  Goes through the same per-request `Context[T]` wrapper as YAML
  routes. Surfaced on `Engine.Routes()` with
  `Source = SourceProgrammatic` so introspection tools and
  `fibermaptest` see them. Panics on programmer errors (invalid
  method, empty name/path, nil handler, called after Mount).
- `RouteInfo.Source` — `"yaml"` or `"programmatic"`. Public
  constants `SourceYAML` and `SourceProgrammatic`. Existing YAML
  routes now report `Source: SourceYAML` in their introspection
  record.

### Deferred
- Hot-reload of `routes.yaml` (`WithHotReload`) — Fiber does not
  support route-table mutation after `app.Add`, so honest hot reload
  requires Listen restart / atomic listener handoff. Design-heavy
  enough to warrant its own release; revisit after v0.4.

## [v0.3.0] - 2026-05-23

A "production defaults" release. Five new `Engine.Run` options
collapse the boilerplate every service was re-writing — `$PORT`
auto-detection for cloud platforms, panic recovery, k8s health
checks, structured access logging, and Prometheus metrics.

### Added
- `Engine.Run` now reads `$PORT` env var when `WithAddr` is not set
  (falls back to `:3000`). Cloud-platform convention (Heroku, Cloud
  Run, fly.io, Railway). `WithAddr` always wins.
- `fibermap.Recover(logger)` + `WithRecover(logger)` — wraps Fiber's
  recover middleware with slog-aware panic logging (method, path,
  request_id, full stack trace) and a 500 response. Installed FIRST
  in the Use chain so panics in any later middleware are caught.
- `WithHealthCheck(path)` — registers a `GET` handler at `path` (e.g.
  `/healthz`) returning 200 OK. Registered BEFORE any middleware so
  it bypasses Recover, RequestID, the user's WithUse chain, and the
  ContextBuilder — exactly what k8s probes need. Default path
  `/healthz`; empty disables. Not surfaced on `Engine.Routes()`.
- `fibermap.RequestLogger(logger, skipPaths...)` +
  `WithRequestLogger(logger, skipPaths...)` — one structured
  access-log line per request: method, path, status, latency_ms,
  response bytes, client IP, request_id. INFO when status < 500,
  ERROR otherwise. `skipPaths` is typically `/healthz` + `/metrics`
  to keep the log clean.
- `fibermap.Metrics()` + `fibermap.MetricsHandler(reg)` +
  `WithMetrics(path)` — Prometheus middleware + scrape endpoint.
  Exposes `fibermap_http_requests_total{method,route,status}` counter,
  `fibermap_http_request_duration_seconds{method,route,status}`
  histogram, and `fibermap_http_requests_in_flight` gauge. The `route`
  label is the Fiber route template (`/v1/tasks/:id`), not the
  concrete path — bounded cardinality. Default path `/metrics`; empty
  disables. Adds `github.com/prometheus/client_golang` to the
  dependency graph.
- `examples/tasks` adopts the full ops bundle: Recover, HealthCheck,
  RequestLogger, Metrics on top of the existing RequestID + Bearer +
  embedded YAML + Cache wiring.

### Changed
- Internal: `ctxKey` switched from `string` ("`__fibermap_ctx__`") to
  a `*byte` sentinel. Cheaper pointer-equality on every `c.Locals`
  call and removes the chance of a user-provided string colliding
  with fibermap's key. No public API change.

## [v0.2.0] - 2026-05-23

A "less boilerplate" release. Headline features: a one-call `Engine.Run`
launcher that hides `fiber.New`/`LoadFile`/`Mount`/`Listen`/graceful
shutdown; first-class `cache:` and `timeout:` YAML fields; the
`fibermap/factory` subpackage of ready-made role/scope guards and
Fiber-handler adapters; query + path-param binding helpers symmetric
to `bind.Body[T]`; and a built-in `fibermap.RequestID()` middleware
so projects stop copying the same eight lines.

### Added
- `fibermap.RequestID()` — built-in Fiber middleware that ensures
  every request carries `X-Request-ID`: reads incoming, generates a
  16-hex-char identifier from `crypto/rand` if missing, stashes the
  value on `c.Locals(fibermap.LocalsRequestID)` for the
  ContextBuilder, and echoes it back in the response header. Wire
  via `WithUse(fibermap.RequestID(), …)` or `app.Use(fibermap.RequestID())`.
  Exposes constants `LocalsRequestID = "request_id"` and
  `HeaderRequestID = "X-Request-ID"`.
- `Engine[T].Run(opts ...RunOption)` — one-call launcher that wraps
  `fiber.New` + `LoadFile("routes.yaml")` + `Mount` +
  `app.Listen(":3000")` plus SIGINT/SIGTERM graceful shutdown (10s
  drain). Defaults are all overridable via options:
  - `WithAddr(addr)` — change the listen address.
  - `WithRoutesPath(path)` — change the YAML filename.
  - `WithRoutesFS(fs.FS)` — load from `embed.FS`.
  - `WithFiberConfig(fiber.Config)` — custom `fiber.New` config.
  - `WithUse(handlers...)` — Fiber-level middlewares installed before
    Mount (run before `ContextBuilder`).
  - `WithConfigureApp(fn)` — escape hatch with raw `*fiber.App`.
  - `WithShutdownTimeout(d)` — drain budget on signal.
  - `WithoutSignalHandling()` — disable Run's signal trap entirely.
  Run skips loading if the engine already has a YAML document
  preloaded. Manual `LoadFile`/`Mount`/`Listen` continues to work.
  Demonstrated in `examples/quickstart`.
- `bind.Query[T]` and `bind.Params[T]` — symmetric to `bind.Body[T]`,
  wrap Fiber's `QueryParser` / `ParamsParser` and run the same
  `Validator` interface. Own sentinel errors: `ErrParseQuery` /
  `ErrValidateQuery`, `ErrParseParams` / `ErrValidateParams`.
- Per-route `timeout:` field in `routes.yaml`. Accepts Go duration
  strings (`"5s"`, `"300ms"`, `"1m30s"`). When set, the handler is
  wrapped with `timeout.NewWithContext` from Fiber: `c.UserContext()`
  gets the deadline, and `context.DeadlineExceeded` surfaces as
  408 Request Timeout. Bad / zero / negative values fail at parse with
  `CodeInvalidTimeout`. Verbatim YAML value surfaced on
  `RouteInfo.Timeout` for introspection. JSON Schema updated.
- New subpackage `fibermap/factory` — ready-made middleware factories
  and Fiber-handler adapters:
  - `RequireRole[T](accessor, opts...)` — role guard.
  - `RequireAnyScope[T](accessor, opts...)` — OAuth-any-of scope guard.
  - `Adapter[T](fiber.Handler)` — bridge a plain Fiber handler into
    `MiddlewareFunc[T]`.
  - `AdapterFactory[T](func(args []string) (fiber.Handler, error))` —
    bridge a parameterized producer into `MiddlewareFactoryFunc[T]`.
  - `WithDenyHandler(h)` — customize the guards' 403 response.
- Built-in **response cache** as a first-class route field — no
  middleware-factory registration required. YAML accepts a scalar
  duration string or a mapping with `ttl`, `control`, `headers`,
  `vary_header`:
  ```yaml
  - method: GET
    path: /reports
    handler: reports.list
    cache: 30s
  - method: GET
    path: /products
    handler: products.list
    cache:
      ttl: 30s
      control: true
      headers: true
      vary_header: [Accept-Language]
  ```
  Engine-wide knobs (`Storage`, `KeyBy`, `MaxBytes`) live on
  `Engine.SetCacheDefaults(CacheDefaults[T]{...})`. Defaults: Fiber's
  in-process map, no `KeyBy` — set `KeyBy` whenever the cached
  response depends on the authenticated user, or you will serve one
  user's body to another. Surfaced on `RouteInfo.Cache` for
  introspection. Bad / zero / negative TTL or empty `vary_header`
  entries fail at parse with `CodeInvalidCache`.
- `fibermap.ContextFrom[T](c *fiber.Ctx) (*Context[T], bool)` — typed
  accessor for the per-request `Context[T]` stashed by fibermap's
  root middleware. Lets factories and adapters that take a plain
  `*fiber.Ctx` (such as cache key generators) read `Data` without
  re-running `ContextBuilder`.
- Indirect dep: `github.com/tinylib/msgp` (pulled by
  `fiber/middleware/cache`).

## [v0.1.0] - 2026-05-22

First tagged release. Includes everything between the original
`Engine[T]`/YAML-routing prototype and a polished DX surface:
parameterized middleware, panic-on-misuse registration, runnable
examples, JSON Schema + CLI, OpenAPI-ready introspection,
test helpers, body binding helper, and a realistic starting template
under `examples/tasks`.

### Added
- Subpackage `fibermap/bind` with `Body[T any](c BodyParser, v Validator) (T, error)`
  — one-liner request-body parse + validate. Talks through minimal
  `BodyParser` / `Validator` interfaces so fibermap itself does NOT
  depend on `go-playground/validator` (or fiber, in the bind tests).
  Sentinel errors `ErrParseBody` / `ErrValidateBody` for branching.
- Realistic starting template at `examples/tasks` — Bearer auth,
  `embed.FS` routes, `slog` logger, in-memory store behind a `Store`
  interface, role-guarded admin endpoints, request-id, graceful
  shutdown, `/admin/routes` introspection over HTTP, and
  `fibermaptest.AssertRoute` covering the live `routes.yaml`. Designed
  to be COPIED, not just read.
- `Engine.Walk(fn)` and `Engine.Lookup(method, path)` for
  introspection. `Walk` visits routes in Mount order; returning
  `ErrStopWalk` ends iteration without surfacing an error. `Lookup`
  returns a `RouteInfo` for an exact (method, path) match. Both return
  introspection. `Walk` visits routes in Mount order; returning
  `ErrStopWalk` ends iteration without surfacing an error. `Lookup`
  returns a `RouteInfo` for an exact (method, path) match. Both return
  defensive copies. These are the building blocks for OpenAPI
  generators and test helpers.
- Subpackage `fibermap/fibermaptest` — `AssertRoute`, `AssertNoRoute`,
  `AssertRouteCount` work off `RouteFinder` (`Lookup`+`Walk`) without
  spinning up a Fiber app or making HTTP requests. Includes
  `WithHandler`, `WithMiddleware` (in-order subsequence),
  `WithTags` options.
- JSON Schema (draft-07) for `routes.yaml` shipped at
  `schema/routes.schema.json`. Add the `# yaml-language-server:
  $schema=…` modeline to your YAML for editor autocomplete + inline
  diagnostics. Schema is also embedded into the library; access via
  `fibermap.Schema()`.
- CLI binary `cmd/fibermap` with `validate <path>` (schema-lint;
  non-zero exit) and `dump-schema` (write embedded JSON Schema to
  stdout). Install via `go install
  github.com/theizzatbek/fibermap/cmd/fibermap@latest`.
- Public `fibermap.Lint(data)` and `fibermap.LintFile(path)` for
  schema-only validation (no registrations needed). Used by the CLI;
  also handy for admin endpoints or pre-commit hooks.
- JSON struct tags on `RouteInfo`, `MiddlewareRef`, and `Error` so
  consumers can expose introspection / structured-log errors without
  wrapping.
- `Engine.Validate()` — run mount-time validation without installing
  any routes. For CI scripts / unit tests that check a `routes.yaml`
  is consistent with the registered handlers/middleware/factories.
- `Engine.LoadFS(fs.FS, path string)` — load YAML from an `io/fs`
  (typically `embed.FS`), so route definitions can ship inside the
  binary.
- `RegisterMiddlewareFactory(name, func(args []string) (MiddlewareFunc[T], error))`
  — parameterized middleware. YAML references factories as a single-key
  mapping `{name: [args...]}`. The factory is invoked once per
  `(name, args)` tuple at Mount time and cached. Factory setup errors
  surface as `CodeInvalidFactoryArgs` in the joined `Mount` error.
- `MiddlewareRef{Name, Args}` — public form of resolved chain entries
  in `RouteInfo.Middleware`.
- Parse errors now carry the source `Line` for missing-field,
  invalid-method, missing-handler, and malformed `middleware:` items.
- Testable doc examples on pkg.go.dev (`Example`,
  `ExampleEngine_RegisterMiddlewareFactory`, `ExampleEngine_Routes`).
- Runnable demo in `examples/quickstart` — `go run .` boots a Fiber
  server on `:3000` with role-aware curl examples.
- GitHub Actions CI: `gofmt`, `go vet`, `go test -race`.
- `CLAUDE.md` for future Claude Code sessions navigating the package.

### Changed
- `roles:` YAML field and the special-cased `SetRoleChecker` /
  `SetForbiddenHandler` mechanism are replaced by user-registered
  parameterized middleware (e.g. a `require_role` factory). See README
  "Parameterized middleware".
- `Register{Handler,Middleware,MiddlewareFactory}` no longer return
  `error` — they panic with `*Error` on duplicate registration
  (MustCompile convention). They also now panic with
  `CodeRegisterAfterMount` if called after `Mount`, where the
  registration would have been silently useless.
- `RouteInfo.Middleware` changes from `[]string` to `[]MiddlewareRef`.
- YAML `middleware:` items are now a heterogeneous list: scalar string
  (plain) or single-key map `{name: [args...]}` (factory). The same
  shape applies inside `middleware_sets`.
- Chain dedup keys on `(name, args)` so the same factory with different
  args coexists in one chain.
- Minimum Go version bumped to 1.26.3 (was 1.23).
- Indirect deps refreshed via `go mod tidy`: fasthttp 1.51→1.71,
  brotli, klauspost/compress, mattn/*, x/sys; added clipperhouse/uax29
  (pulled by updated uniseg/fasthttp); dropped unused uniseg, tcplisten.

### Removed
- `SetRoleChecker`, `SetForbiddenHandler`, `RoleChecker[T]`,
  `ForbiddenFunc[T]`.
- `CodeMissingRoleChecker` (no longer applicable).
- `__role_guard__` sentinel from the resolved chain.
- `RouteInfo.Roles` and `rawRoute.Roles`.