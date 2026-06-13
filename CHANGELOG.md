# Changelog

All notable changes to fibermap. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/). From
`v1.0.0-rc1` onward, the kit follows the semver promises documented
in [`docs/versioning.md`](docs/versioning.md). Pre-v1 history is
archived in [`docs/CHANGELOG-0.x.md`](docs/CHANGELOG-0.x.md).

## [Unreleased]

### Added
- `service.WithErrorHandler(fiber.ErrorHandler) Option` — overrides
  the default `fibermap.ErrorHandler(logger)` install in
  `service.buildFiberConfig`. The typical use case is sentrykit 5xx
  auto-capture wired as:

      service.WithErrorHandler(
          sentrykit.WrapErrorHandler(fibermap.ErrorHandler(logger)))

  Before this option, the only path to override the kit-default
  ErrorHandler was `WithRunOptions(fibermap.WithFiberConfig(...))`,
  which clobbered the caller's body-limit and any other fiber
  config the kit had wired. `WithErrorHandler` composes cleanly
  with `WithBodyLimit` (both fields merge into the single
  buildFiberConfig output) and with the rest of the kit-default
  fiber.Config.

  Nil argument is "equivalent to never calling WithErrorHandler at
  all" — the kit default still installs. Caller-supplied
  `fibermap.WithFiberConfig` via `WithRunOptions` continues to win
  over this option (it's applied later in the RunOption chain), so
  use either WithErrorHandler OR a custom fiber.Config, not both.

  Surfaced by the first integrator (LicenseKit, P2-16 in
  [`docs/v1-followup-licensekit.md`](docs/v1-followup-licensekit.md)).
  Originally proposed as cleanup work alongside the v1.0.1 P0-3
  ErrorHandler / BodyLimit decoupling; the option-form lands now
  as an additive surface in v1.1.0.
- `audit/auditfm` — new subpackage wiring `audit` into `fibermap`
  handler registration. `auditfm.Wrap[T](logger, spec, fn) HandlerFunc[T]`
  decorates a fibermap handler with post-execution audit emission;
  the spec declares Action / SubjectFn / TargetFn / MetadataFn /
  OutcomeFn next to the handler at registration:

      fibermap.RegisterHandler(eng, "license.revoke",
          auditfm.Wrap[AppCtx](svc.Audit, auditfm.Spec{
              Action: "license.revoke",
              SubjectFn: func(c *fiber.Ctx) string { ... },
              TargetFn: func(c *fiber.Ctx) audit.Target { ... },
          }, h.RevokeLicense),
      )

  Default outcome classifier maps nil → Success,
  `*errs.Error{Kind: Unauthorized|Permission}` → Denied, anything
  else → Failure. Override via Spec.OutcomeFn for domain-specific
  rules.

  Emission uses a Background-derived context so the audit append
  outlives the request ctx (which may already be Done by the time
  the response writes complete). Audit-store failures surface via
  Spec.Logger.Warn — they never bubble back to the handler path.
  Nil *audit.Logger panics at registration time (gap must surface
  loud); empty Spec.Action skips silently with a warn.

  For typed-bind handlers (RegisterHandlerWithBody / WithParams /
  WithInput / etc), the lower-level `auditfm.Emit(c, logger, spec, err)`
  is the inline building block — same emission contract, called
  manually inside the typed handler after deciding outcome.

  Complements (does not replace) `audit/auditmw` — auditmw stays
  the app-level "blanket policy" middleware, auditfm is the
  per-handler precision adapter.

  Surfaced by the first integrator (LicenseKit, P2-15 in
  [`docs/v1-followup-licensekit.md`](docs/v1-followup-licensekit.md))
  who needed to wire audit on six mutating endpoints in six lines
  rather than sixty.
- `service.BootOption`, `service.WithSubcommand(name, fn)`,
  `service.BootSeed(name, fn)` — subcommand routing for the
  existing `service.Boot` main()-reducer. When the first CLI
  argument matches a registered subcommand name, `Boot` dispatches
  to that handler instead of the default `fn`. Subcommand fns
  share the same `func(ctx context.Context) error` signature as
  the default and receive the same signal-aware ctx, so SIGINT /
  SIGTERM during a long-running seed / migrate operation
  propagates cleanly. Unknown args fall through to the default fn.
  Last-write-wins on duplicate-name registration.

  `BootSeed` is a named alias for `WithSubcommand` under the
  common "seed mode" case — `BootSeed("seed", fn)` reads better
  in main.go than `WithSubcommand("seed", fn)`. Identical
  semantics; use `WithSubcommand` directly for non-seed names
  (`migrate`, `inspect`, `schema-dump`, etc).

  The kit deliberately does NOT pre-construct a Service for
  subcommand fns — Boot can't carry type parameters, and CLI
  paths typically want different option sets (`WithoutCron`,
  `WithoutOpenAPI`, but `WithMigrations`) than the production run
  path. Each subcommand fn owns its own `service.New` invocation.

  Surfaced by the first integrator (LicenseKit, P2-14 in
  [`docs/v1-followup-licensekit.md`](docs/v1-followup-licensekit.md))
  who was rebuilding the `cmd/licensekitctl seed | migrate`
  subcommand-dispatch boilerplate in every kit-based service.
- `service.WithExtraValidators(map[string]validator.Func) Option`
  — registers tag-name → validator.Func pairs ON the kit-default
  `*validator.Validate` instance that `service.New` builds when
  `WithValidator` was NOT passed. Solves the "kit defaults + one
  custom tag" case without forcing callers to reconstruct
  `validator.New(validator.WithRequiredStructEnabled())` from
  scratch. Multiple `WithExtraValidators` calls accumulate into a
  single map; later calls overwrite same-tag registrations
  (last-write-wins). Empty / nil maps are no-ops. When both
  `WithValidator` and `WithExtraValidators` are passed, the
  caller-supplied validator is used verbatim and extras are
  silently ignored — the kit refuses to mutate a caller-shared
  instance.

  Also adds `service.CodeExtraValidatorRegister` for defensive
  wrap of validator.RegisterValidation errors at boot, and a new
  read-only `fibermap.Engine.Validator() bind.Validator` accessor
  so service-level tests can introspect the wired validator
  (mirrors the existing `SetValidator`).

  Surfaced by the first integrator (LicenseKit, P2-12 in
  [`docs/v1-followup-licensekit.md`](docs/v1-followup-licensekit.md))
  who needed a `slug_chars` validator that's safe across all DB
  backends.
- `service.ServiceConfig.CORSOrigins string` + env `CORS_ORIGINS`.
  Comma-separated list of allowed origins. When non-empty AND no
  `WithCORS` / `WithCORSConfig` option was passed by the caller,
  `service.New` applies `WithCORS(origins...)` with the kit-defaulted
  `cors.Config` automatically. Whitespace around each comma-separated
  entry is trimmed and blank entries are dropped (so `" , a.com,, "`
  parses as `["a.com"]`). `AllowCredentials` matches the WithCORS
  contract: enabled when every origin is explicit, auto-disabled
  when `*` appears (per the CORS spec rejecting
  `Access-Control-Allow-Origin: *` together with credentials).
  Caller-supplied `WithCORS` / `WithCORSConfig` always wins —
  detection rides on a new `options.corsWired` flag flipped by both
  option constructors. The env auto-enable path only covers the
  kit-defaulted shape; custom headers / `AllowOriginsFunc` /
  non-default `MaxAge` still require an explicit `WithCORSConfig`.
  Surfaced by the first integrator (LicenseKit, P2-11 in
  [`docs/v1-followup-licensekit.md`](docs/v1-followup-licensekit.md)).
- `auth.GenerateAPIKey(pepper []byte) (plain, hash []byte, prefix string, err error)`
  — kit-standard recipe for minting fresh API keys. Returns the
  triple every service was reinventing: `plain` (`ak_<28-char
  base64-RawURL>`, 31 chars total — show to user once at issue
  time and drop), `hash` (HMAC-SHA256(plain, pepper) — store in
  the [KeyStore]), and `prefix` (first 8 chars of plain — safe to
  surface in admin UIs without revealing the rest). pepper MUST
  equal `auth.Config.APIKeyHashSecret` (or the
  `auth.WithAPIKeyHashSecret` override) so the issued plain
  resolves against the same HMAC chain at verify time. Errors:
  `*errs.Error{Code: CodeKeygenBadPepper}` on pepper < 32 bytes,
  `*errs.Error{Code: CodeKeygenEntropy}` on system PRNG failure.
  New `auth.APIKeyPrefix` constant ("ak_") exposes the canonical
  marker for callers that need to parse the prefix back out.
  Surfaced by the first integrator (LicenseKit, P1-10 in
  [`docs/v1-followup-licensekit.md`](docs/v1-followup-licensekit.md)).
- `gokit/ids` — new public top-level package for prefixed ULIDs.
  `ids.New("user_")` mints a time-sortable `user_<26-char Crockford-
  Base32>` string; `ids.Parse(prefix, s)` validates the prefix and
  the 26-char ULID suffix, returning the raw `[16]byte` ready to
  INSERT into a Postgres `uuid` column (uses the v1.0.1 P1-8
  `[16]byte ↔ uuid` codec automatically); `ids.Format(prefix, raw)`
  is the inverse for callers holding bytes scanned from a uuid
  column. `New` is goroutine-safe — a package-level mutex serialises
  access to the monotonic-entropy source so concurrent same-ms
  calls produce strictly increasing IDs per the ULID spec.

  Declarative DTO validation via `ids.RegisterValidator(*validator.Validate)`
  wires the `validate:"id_prefix=prod_"` struct tag — fields failing
  the tag flow through `fibermap.ErrsvalBindError` as per-field
  `Details[]` in the 400 response without any extra wiring. The
  `ids.Tag(prefix)` helper assembles the tag string programmatically
  for code-generated DTOs.

  Stable error codes: `CodeBadPrefix` / `CodeBadSuffix`. Sentinel
  errors `ErrBadPrefix` / `ErrBadSuffix` exist for `errors.Is` but
  most callers should match on `e.Code` (semver-stable).

  Surfaced by the first integrator (LicenseKit, P1-6 in
  [`docs/v1-followup-licensekit.md`](docs/v1-followup-licensekit.md)).
  Closes the per-service `internal/domain/ids.go` reinvention loop.
- `gokit/crypto` — new public top-level package for at-rest sealing.
  Two surfaces: `crypto.MasterKey` (single AES-256-GCM key, wire
  format `[version=0x01] [nonce(12)] [ct+tag]`) and `crypto.Keychain`
  (kid-routed multi-key sealing for rotation; wire format
  `[version=0x02] [kid(1)] [nonce(12)] [ct+tag]`). Constructors
  accept raw 32-byte keys or base64-encoded strings (every Go stdlib
  flavour: std/url, padded/raw). All failure modes return
  `*errs.Error` with stable codes (`CodeKeyLength` / `CodeKeyBase64`
  / `CodeKeychainEmpty` / `CodeKeychainNoActive` / `CodeSealNonce`
  / `CodeCiphertext`). `CodeCiphertext` collapses every Open failure
  (short blob, unknown version, unknown kid, AEAD tag mismatch) into
  a single code so callers don't leak the specific failure cause to
  upstream consumers.

  `clients/webhooks/storepg`'s private `crypto` helper is rewritten
  as a thin wrapper over `gokit/crypto.MasterKey` that re-tags the
  underlying error codes as `webhooks.CodeStorepgNoKey` /
  `webhooks.CodeStorepgDecryptFailed` so existing alerting rules
  keep matching. New consumers should reach for `gokit/crypto`
  directly. Surfaced by the first integrator (LicenseKit, P1-5 in
  [`docs/v1-followup-licensekit.md`](docs/v1-followup-licensekit.md))
  who was rewriting AES-GCM Seal/Open inline in every service.
- `fibermap.ErrsvalBindError[T any]` — the recommended
  `SetBindErrorHandler` value for services using the kit's typed
  `*errs.Error` contract for HTTP failure modes. Maps every
  `bind.Body / Query / Params / Header` parse / validate failure
  (and `RegisterHandlerWithInput`'s composed failures) into the
  kit's standard `{code, message, details[]}` wire shape with a
  source-aware code (`invalid_body` / `invalid_query` /
  `invalid_params` / `invalid_header`; `invalid_request` for
  anything else). Per-field `details[]` come from
  `errsval.FromValidator` walking the `validator.ValidationErrors`
  chain that the v1.0.1 P0-1 fix preserves via `errors.Join`. Wire
  once on engine setup: `eng.SetBindErrorHandler(fibermap.ErrsvalBindError[AppCtx])`.
  Kit `defaultBindError` stays the simple `{"error": err.Error()}`
  baseline so the `fibermap` package itself doesn't force the
  errs convention. Surfaced by the first integrator (LicenseKit,
  P1-4 in [`docs/v1-followup-licensekit.md`](docs/v1-followup-licensekit.md)).
- `auth.WithAPIKeyHashSecret([]byte) Option` — Option-form for
  supplying the APIKey middleware's HMAC pepper, parallel to the
  existing `Config.APIKeyHashSecret` field. Both paths feed the
  same `Auth.apiKeyHashSecret` slot at construction time; the
  Option WINS over the Config field when both are set, so a
  caller threading the secret through `service.WithAuthOptions`
  can always trump a stale env value if needed. Empty / nil
  Option defers to Config. Surfaced by the first integrator
  (LicenseKit, P1-9 in
  [`docs/v1-followup-licensekit.md`](docs/v1-followup-licensekit.md)).
- `service.AuthConfig.APIKeyHashSecret string` + env
  `AUTH_APIKEY_HASH_SECRET`. The env value is base64-decoded
  (every Go stdlib flavour accepted: std / URL-safe, padded / raw)
  and must yield ≥ 32 bytes after decode. `service.New` returns
  `*errs.Error{Code: CodeAuthInvalidAPIKeyHashSecret}` on a bad
  or too-short value — surfacing a misconfig at boot instead of
  at the first API-key request via the old runtime
  `CodeAPIKeyMissingSecret` panic. The decoded bytes thread to
  `auth.New` via `auth.WithAPIKeyHashSecret(...)`. Pure-JWT
  services without API-key middleware can leave the field unset.
  Surfaced by LicenseKit P0-2.

### Documentation
- `service/README.md` — new "Production deployment checklist"
  subsection right after Quickstart. MUST / SHOULD / OPTIONAL
  grouping covers the v1.0.1-fixed behaviours (typed error wire
  shape, env-driven Sentry/OTel auto-enable), the eternal-by-design
  semantics (`WithValidator` replace-not-extend, `WithBodyLimit`
  vs caller `WithFiberConfig` interaction), and the v1.1.0 roadmap
  fillers (CORS env, `ErrsvalBindError`). Surfaced by LicenseKit
  D-1 in [`docs/v1-followup-licensekit.md`](docs/v1-followup-licensekit.md).
- `docs/common-gotchas.md` — new compact reference of 15
  trip-wires with Status column showing which kit version
  eliminates each (4 closed in `v1.0.1`, 8 pending in `v1.1.0`,
  3 eternal). Cross-links to the LicenseKit followup doc and to
  `docs/versioning.md`. Surfaced by LicenseKit D-2.
- `README.md` + `fibermap/README.md` — Quickstart now demonstrates
  `fibermap.RegisterHandlerWithBody` immediately after the raw
  `RegisterHandler` example, with a `validate:` tag on the input
  struct, `WithResponse` schema attach and a pointer at the
  sibling `RegisterHandlerWith{Query,Params,Headers,Input}`
  helpers. The typed surface was always the kit's recommended
  shape for real endpoints but the Quickstart was steering
  readers at the raw `*fibermap.Context[T]` first. Surfaced by
  LicenseKit D-3.

### Changed
- `service.New` — Sentry and OTel now auto-enable from environment
  variables when the caller did not opt-in via `WithSentry` /
  `WithOtel`. New behaviour:
  - `SENTRY_DSN` (non-empty) → wires Sentry with default
    `SentryOptions{}` if `WithSentry` was not called.
  - `OTEL_EXPORTER_OTLP_ENDPOINT` or
    `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` (non-empty) → wires OTel
    with default `OtelOptions{}` if `WithOtel` was not called.
    Service name resolves in order: `OTEL_SERVICE_NAME` (W3C
    standard) → `cfg.Service.ServerGroup` → `cfg.Service.NodeName`
    → skip when none of the three is set.
  - `OTEL_SDK_DISABLED=true` (W3C-standard kill switch) skips OTel
    auto-enable regardless of endpoint env.
  Caller-supplied `WithSentry` / `WithOtel` always wins — the env
  lookup only fires when the corresponding option slot is still
  empty. Existing services that already wire both options
  programmatically see no behaviour change. Surfaced by the first
  integrator (LicenseKit, P2-13 in
  [`docs/v1-followup-licensekit.md`](docs/v1-followup-licensekit.md))
  who was rewriting the same `os.Getenv("SENTRY_DSN")` boilerplate
  in every service.

### Fixed
- `db.Connect` — raw `[16]byte` query arguments now encode to Postgres
  `uuid` columns (OID 2950) without a `pgtype.UUID{Bytes: b, Valid: true}`
  wrap, and scan back into `[16]byte` destinations symmetrically.
  Pre-fix, pgx5 returned `unable to encode 0x.. into binary format
  for uuid (OID 2950): cannot find encode plan` and every kit
  consumer rewrote a private `uuidArg(b)` helper. pgx's `UUIDCodec`
  already handles `[16]byte` natively in both directions; the kit
  hole was the per-connection `TypeMap`'s missing default-pg-type
  registration. `db/conn_init.go::composeAfterConnect` now installs
  the `[16]byte` → `uuid` default-mapping unconditionally on every
  fresh connection, before the per-conn `statement_timeout` setter
  and any caller-supplied `WithConnInit` hooks. Surfaced by the
  first integrator (LicenseKit, P1-8 in
  [`docs/v1-followup-licensekit.md`](docs/v1-followup-licensekit.md)).
- `fibermap/bind.{Body,Query,Params,Header}` — preserve the wrapped
  inner error's type, not just its string. The previous wrap was
  `fmt.Errorf("%w: %v", ErrValidateBody, err)`: `%w` chained the
  sentinel, `%v` stringified the underlying error, so downstream
  `errors.As(err, &concreteType)` could never recover the real type.
  Most painfully this broke `errsval.FromValidator` — it could not
  reach `validator.ValidationErrors`, and the whole point of
  `errsval` (per-field `Details[]` in the wire body) was unreachable
  from any bind-wrapped error. Switched to `errors.Join(sentinel,
  err)` so both `errors.Is` (existing contract — still works) AND
  `errors.As` (the bug) succeed. Surfaced by the first integrator
  (LicenseKit, P0-1 in
  [`docs/v1-followup-licensekit.md`](docs/v1-followup-licensekit.md)).
  Note that `err.Error()` now joins the sentinel and inner message
  with a newline instead of `": "` — callers logging the bare
  string see a two-line trace; structured callers using
  `errsval.FromValidator` get the correct per-field `Details[]`.
- `service.Run` — `fibermap.ErrorHandler` is now installed
  unconditionally instead of being gated by `WithBodyLimit(>0)`.
  Pre-fix, a caller that wired `service.New` without an explicit
  body-limit silently fell back to fiber's default plain-text 500
  on every typed `*errs.Error` return. The wire shape
  `{code, message, details[]}` documented everywhere else in the
  kit was unreachable without remembering to set `WithBodyLimit`.
  Fix: `service.(*Service).buildFiberConfig` always seeds
  `fiber.Config{ErrorHandler: fibermap.ErrorHandler(logger)}` and
  only overlays `BodyLimit` when the option supplied a positive
  value. Surfaced by the first integrator (LicenseKit, P0-3 in
  [`docs/v1-followup-licensekit.md`](docs/v1-followup-licensekit.md)).

## [v1.0.0] - 2026-06-11

First stable major. Promoted from `v1.0.0-rc1` on the same day —
the planned ≥ 1-week bake in `examples/` was waived; race regressions
(if any slipped through the CI-restructure) will surface via the
nightly `race.yml` workflow and be addressed in `v1.0.x` patch
releases. The contract from here forward is documented in
[`docs/versioning.md`](docs/versioning.md): every change against
this tag is classified MAJOR / MINOR / PATCH per the rules there.

For the full pre-v1 audit-close list (`P0` / `P1` / `P2` items and
their closing commits), see [`docs/v1-readiness.md`](docs/v1-readiness.md).

## [v1.0.0-rc1] - 2026-06-11

Release candidate for the first stable major. Everything in this
section was previously tracked under `[Unreleased]` and tagged as
"pre-v1" — those qualifiers are now the contract: the listed
breaking changes are the final pre-v1.0 surface, and from this
tag onward semver applies per [`docs/versioning.md`](docs/versioning.md).
See [`docs/v1-readiness.md`](docs/v1-readiness.md) for the full
P0/P1/P2 audit-close list that fed this candidate.

Promoted to `v1.0.0` later the same day with no further changes;
see the `[v1.0.0]` section above.

### Removed (breaking, pre-v1)
- `db.(*DB).ReadPool() *pgxpool.Pool` and `db.(*DB).HasReadReplica() bool`
  — single-pool back-compat accessors. Both predate the multi-replica
  refactor; new code should use `(*DB).ReadPools()` (the full set with
  names, health and lag) or `len(d.ReadPools()) > 0` for the boolean
  check. Dropped ahead of v1 to keep the stable surface lean.
  `Config.HasReadReplica` (env-driven knob that opens a standby pool
  against the same host) stays — it is not back-compat, just an
  alternative to `Config.ReadURLs` for the single-replica case.
- `auth.SetPrincipalForTest[C]` — moved out of the production
  `auth` package into the sibling `auth/authtest` subpackage as
  `authtest.SetPrincipal[C]`. Production code should never have been
  able to call a `ForTest`-suffixed function from a regular import;
  splitting it off makes accidental production use surface in greps
  and code review. The Locals key was relocated to an internal
  helper package (`auth/internal/principalkey`) so the sibling can
  write to the same slot Bearer / API-key / session middleware reads
  from, without exposing the key to external callers. Replace
  `auth.SetPrincipalForTest[C](c, p)` with
  `authtest.SetPrincipal[C](c, p)` and import
  `github.com/theizzatbek/gokit/auth/authtest`.
- `auth/sessions.StoreStats.Expired` removed from the public
  rollup type. The field could only be honestly populated by
  `MemoryStore`, which has no eviction; both production-grade
  backends (auto-evicting `sessionsredis.Store` and any future
  TTL-aware backend) lose expired rows before Stats sees them, so
  the field was effectively always-zero or race-zone for anything
  except the dev store. The cross-backend contract now is
  `StoreStats{Active, Total}`: Active = rows with
  `ExpiresAt > now` and still enumerable; Total = every
  enumerable row. Callers that need to act on
  expired-but-not-yet-evicted rows should reach for
  `Lister.ListBySubject` and filter on `Session.ExpiresAt`
  themselves. Update any `stats.Expired` reads to that pattern.

### Changed (breaking, pre-v1)
- `clients/redis.(*Client).Redis() *redis.Client` now **panics** with
  a guiding message under cluster / sentinel topologies instead of
  silently returning `nil`. The old behaviour was an API trap: a
  caller asking for the single-mode type under cluster/sentinel
  would dereference nil far from the call site. The panic message
  names the actual mode and points at `Client.Universal()` as the
  cross-mode escape hatch. Nil-receiver behaviour is unchanged
  (still returns nil).
- `clients/ratelimit.NewRedis` now returns
  `*errs.Error{Code: CodeInvalidConfig}` instead of panicking
  through `rc.Redis()` when the passed `*redisclient.Client` runs in
  cluster / sentinel mode. The Lua sliding-window script is
  single-mode-only by design (all keys pin to one node); the early
  validation surfaces the constraint at construction time.
- `fibermap/sse.Stream` now CAS-guards `Send` / `SendJSON` /
  `Comment` against concurrent use. The doc-string has stated
  "not safe for concurrent use" since the package shipped, but
  there was no runtime detection — a second goroutine sneaking in
  would either corrupt the wire frame (interleaved `event:` /
  `data:` lines) or double-flush the bufio buffer. The CAS guard
  fails loudly: the second caller panics with a guiding message
  naming the offending method, pgx-style. Sequential reuse from
  one goroutine (the canonical happy path) is unaffected.
- `cronmap.(*Runtime).TriggerJob` now requires a third explicit
  argument: `cronmap.OverrideOK{}`. The method bypasses both the
  singleton (leader-election) lock AND the per-job pause flag —
  intentional for /admin force-run actions, but the bare
  `TriggerJob(ctx, "name")` signature gave no signal of that at
  the call site, and an /admin endpoint forwarded straight from
  HTTP could shred leader-election invariants without anyone
  noticing on review. The empty-struct token has zero runtime
  cost; its purpose is to surface "I know this bypasses cluster
  safety" in greps + code review. Replace
  `rt.TriggerJob(ctx, "x")` with
  `rt.TriggerJob(ctx, "x", cronmap.OverrideOK{})`.

### Documentation
- `sentrykit` — CPU profiling status re-confirmed for v1 freeze.
  Re-checked `getsentry/sentry-go` upstream on 2026-06: v0.46.2
  is still the latest tagged release (`go list -m -versions`
  shows no newer), the changelog of the most recent 5 releases
  mentions no profiling API, and the long-standing tracking
  issue ([sentry-go#630](https://github.com/getsentry/sentry-go/issues/630))
  is closed without an API ever landing. Deferral is therefore
  not a "kit didn't finish wiring it" gap — it is "the upstream
  SDK doesn't expose the knob to wire." Kit will add hooks the
  moment `ClientOptions` grows a `ProfilesSampleRate` (or
  successor) — additive change, not breaking. README updated to
  spell this out, point at the upstream issue, and suggest the
  three sidecar paths (Pyroscope, Grafana Phlare over OTLP,
  `net/http/pprof` on an internal port) that work alongside the
  kit's `WithSentry` in the meantime.

- `clients/nats.(*Client).Conn()` / `JetStream()` — escape-hatch
  contract spelled out ahead of v1. The old doc-string warned
  that errors don't get *errs.Error wrapping but stopped there;
  the full passthrough list was tribal knowledge. The doc-string
  + README now enumerate every layer the kit deliberately does
  NOT apply to direct-handle calls: error-mapping, Prometheus
  `nats_*` collectors, breaker / default-timeout, W3C
  TraceContext injection — all bypassed when callers reach for
  the raw conn instead of `Publish` / `PublishViaCodec` /
  `PublishRaw` / `Subscribe`. Also spells out lifecycle: Close
  on the underlying conn is kit-owned, callers MUST NOT call
  `c.Conn().Close()` / `c.Conn().Drain()` directly (that
  bypasses the idempotent `Client.Close` and desynchronises
  internal state). `JetStream()` returns nil in core-only mode —
  nil-check before use. v1 contract: signatures stable,
  passthrough semantics stable; missing behaviours should land
  as new typed methods, not as wrapping retrofitted onto these
  hatches.

- `clients/natsmap/natsgw` — README expanded with two missing
  sections ahead of v1: **Observability** (gateway carries no
  collectors / logger of its own — handler sits behind Fiber's
  observability stack; HTTP-side metrics from
  `service.WithFiberMetrics`, span coverage from `otelfiber`,
  NATS-side `natsmap_publish_total{publisher,outcome}` + duration
  histogram cover gateway publishes the same as direct in-process
  ones; W3C TraceContext auto-injects into NATS headers via
  `natsclient.publishBytes`) and **Когда НЕ использовать**
  (critical-path low-latency from a Go service that can import
  natsmap directly; at-least-once expectations without an outbox
  in `WithCustomHandler`; multi-tenant ingestion where subject
  allowlist alone can't gate cross-tenant publishes). Parent
  `clients/natsmap/README.md` now cross-references the gateway as
  well, so readers landing on the natsmap overview see the
  HTTP-fronted option without grepping for it.

- `clients/webhooks.WorkerConfig.Propagator` — clarified the
  contract ahead of v1 freeze. The field is the single source of
  truth for outbound tracing header injection: the worker calls
  `Propagator.Inject(ctx, req.Header)` once per attempt, whatever
  headers the propagator emits land verbatim. nil (default) means
  no tracing headers — the kit does NOT fall back to a built-in
  propagator, does NOT silently call `otel.GetTextMapPropagator()`,
  does NOT read any global state. The recommended config remains
  `cfg.Propagator = otel.GetTextMapPropagator()` (W3C TraceContext
  + Baggage composite), matching otelhttp + clients/nats. v1
  freezes this knob as the only tracing injection point on a
  Worker: no side-channel `B3Propagator` / `DatadogHeaders` fields
  in v1 minors — multi-format callers compose
  `propagation.NewCompositeTextMapPropagator(...)` and pass the
  composite. Doc-string + README updated; no code change.

### Fixed
- `fibermap/wsnats.runBridge` now unblocks its WS read promptly on
  cancellation. Previously, after the loop's per-connection ctx
  was cancelled (subscription callback errored, parent ctx done,
  OnMessage returned an error) the main goroutine stayed parked
  inside `ws.ReadMessage` until the client happened to send a
  frame — a silent client meant the read goroutine stayed alive
  indefinitely and the subscription-unsubscribe chain never ran.
  The bridge now spawns a small cleanup goroutine that calls
  `ws.SetReadDeadline(time.Now())` the moment the loop ctx fires,
  forcing an immediate timeout error on the in-flight read so the
  main loop bubbles up cleanly. Also documents the kit's
  concurrency contract explicitly in `doc.go` + README: reads are
  owned by the kit's main goroutine and exclusively so; callers
  in BridgeFn / OnMessage / OnFrame must NOT spawn their own
  goroutines that read from the *websocket.Conn (writes were
  already covered by an internal mutex).

### Added
- v1 P2 bucket — small additive surface, housekeeping, and doc
  clarifications consolidated ahead of the v1 freeze:
  - `auth/refreshredis.WithStatsCap(n)` + sentinel
    `refreshredis.ErrStatsCapExceeded`. `Stats()` is O(N) by
    design (SCAN + pipelined HMGET, admin path) and was previously
    unbounded — fine for a diagnostic shell, a foot-gun for an
    /admin endpoint when the keyspace grows. The cap discards
    partial counts and returns the sentinel so callers branch via
    `errors.Is` and choose between widening the cap and re-scoping
    via `ListBySubject`.
  - `bulkhead.Config.OnAcquireFail func(reason string)` — symmetric
    to `OnCapacityChange`. Fires on every reject path with the
    same reason label the Prometheus collector observes
    (`full` / `ctx_canceled` / `queue_timeout`). Use to drive
    domain-level circuits (mark upstream sick, switch to fallback)
    from the same signal the metrics see, without scraping. Panic-safe.
  - `sentrykit.WithExtraScrubHeaders(headers ...string)` —
    `ScrubOption` for `ScrubPII()` and `WithoutPII()` that extends
    the default redaction set (`Authorization`, `Cookie`,
    `X-API-Key`, `Set-Cookie`) with app-specific headers
    (`X-Internal-Token`, `X-Vault-Lease`, …). Case-insensitive
    matching; multiple calls accumulate. The zero-option call
    sites `ScrubPII()` / `WithoutPII()` keep their previous
    behaviour.
  - `service.NewSimple(ctx, cfg, opts...)` — zero-type-parameter
    shortcut for `service.New[struct{}, struct{}]`. Saves the
    noisy `[struct{}, struct{}]` instantiation when the caller
    carries neither a typed fibermap-payload nor typed JWT claims.
  - `CODE_OF_CONDUCT.md` (Contributor Covenant 2.1) + GitHub
    issue templates (`.github/ISSUE_TEMPLATE/{bug_report.yml,
    feature_request.yml,config.yml}`) — repo housekeeping
    expected for public OSS at v1.
  - CHANGELOG split: every released `[v0.x.y]` section archived to
    [`docs/CHANGELOG-0.x.md`](docs/CHANGELOG-0.x.md) at the v1
    freeze. The main `CHANGELOG.md` now keeps `[Unreleased]` +
    every section since the most recent release.
  - Verified items (no code change, doc-only): `clients/cache.JSONCodec`
    is already exported as the default; `clients/apimap`
    README already documents that `RegisterTransport`'s mock mode
    preserves the breaker/bulkhead chain; `db/migrate.Generate`
    README already covers `WithTimestamp()` vs default
    next-NNNN modes; `cronmap` README already nails the 5-field
    default + `WithParser` seconds-precision opt-in.
  - `bulkhead.README` adds an `OnAcquireFail` section symmetric to
    `OnCapacityChange`; `service.README` adds a `NewSimple`
    one-liner pointer; `sentrykit.README` shows the extra-header
    form on both `ScrubPII()` and `WithoutPII()`; refreshredis
    `Stats` table row spells out the cap+sentinel pair.

- `db/testdb.BootCluster(ctx, replicas, opts...) (*Cluster, func(), error)`
  — lower-level cluster bootstrap helper that mirrors what
  SpinCluster wraps, minus the `*testing.T` coupling. Returns the
  Cluster handle plus a teardown closure the caller owns. Designed
  for `TestMain` — `SpinCluster` boots a fresh cluster on every
  call (~15-30s) because cross-test replication state does not
  isolate cleanly the way schema namespaces do, which adds up
  fast in packages with multiple cluster-based tests;
  `BootCluster` lets such packages share one cluster across the
  whole binary and pay the boot cost exactly once. Trade-off is
  explicit in the doc-string + README: caller owns cross-test
  isolation (TRUNCATE rows between tests, watch for WAL /
  pg_stat_replication leaks, recreate schemas after DDL).
  `teardown` is non-nil even when err != nil — partial boots
  still need it called. The `SpinCluster` doc-string + package
  README now also call out the performance-trap of per-call boots
  explicitly so readers reach for `BootCluster` before adding
  their tenth `SpinCluster` call in a row.

- `bulkhead.VegasController` — second built-in implementation of
  the `Controller` extension point, joining `AIMDController`. A
  TCP-Vegas-inspired control law that learns a baseline latency
  (the monotone floor of observed P50s) and estimates queue length
  from the ratio `currentP50 / baseline`. Grows capacity when the
  estimate is below Alpha (default 2), shrinks when above Beta
  (default 6), holds in between. Hard error spikes still trigger a
  multiplicative cut (`cap/2`, floor 1) on top of the latency loop
  — TCP Vegas itself does not handle loss-style signals, but in a
  service-mesh context 5xx + cancellations carry the same distress
  meaning. Better than AIMD when the downstream degrades
  gradually (DB pool exhaustion, slow upstream) — latency rises
  long before errors do; AIMD remains the right pick when the
  downstream fails-fast and latency carries little signal. The
  extension point now ships with two distinct laws rather than one
  and a TODO; new algorithms (Gradient2, etc.) still slot in
  behind `Controller.Next(Snapshot) int`.

- `service.Service` gains typed `MustX` / `OptionalX` accessor
  pairs for every optional subsystem (`DB`, `Auth`, `NATS`,
  `Redis`, `NATSMap`, `APIMap`, `Hasher`, `Outbox`, `S3`,
  `CronMap`, `RateLimiter`, `WebhooksWorker`, `WebhooksFanout`).
  `MustX` panics with a guiding message that names the Config
  knob or option which would have wired the subsystem;
  `OptionalX` returns `(subsystem, ok)` for ergonomic explicit
  nil-checks. The original public fields stay exported — existing
  call sites like `svc.DB.Query(...)` compile unchanged.

- `batch.Config.IsRetryable func(error) bool` — opt-in classifier
  to break the retry budget early when HandlerFn returns a
  permanent error. nil (default) uses a built-in that treats
  `context.Canceled` and `context.DeadlineExceeded` as
  non-retryable and everything else as retryable — the right
  default for a batch pipeline whose dispatchCtx is the caller's
  signal to stop working. Also fixes a pre-existing bug in the
  retry loop: a `break` inside `select case <-dispatchCtx.Done()`
  only exited the select, so a cancelled dispatch would still
  call HandlerFn one more time before bailing. The loop now uses
  a labeled break so cancellation exits the retry chain
  immediately. Existing call sites are unaffected by the new
  field — leave `IsRetryable` zero and behaviour matches the new
  default (no retry on ctx-cancellation; full retry on every
  other error).

- `db/migrate/` — four additive helpers around the existing runner.
  Existing Up / UpTo signatures gain variadic `Option`s but stay
  back-compat (old call sites compile unchanged).
  - `WithLock(name)` Option on Up / UpTo wraps the apply loop in an
    advisory lock so concurrent boot races (k8s rollout, HPA
    scale-up) don't trip duplicate-key inserts on
    `schema_migrations`. One replica acquires; the rest block, then
    drain a now-applied set as a no-op. Lock key derives from
    sha256(name); empty name → no lock (back-compat). Session-level
    lock auto-releases when the conn returns to the pool — a crash
    mid-apply doesn't strand the lock. Powered by `db/lock`.
  - `DryRun(ctx, d, fsys, w)` writes a human-readable plan of the
    pending Up migrations to w without executing any SQL. Output
    shape: header + per-migration filename + body. Use as a CI
    pre-flight gate or as the body of a `kit migrate plan`
    subcommand. `Pending(ctx, d, fsys)` is a thin alias for
    `Plan` — read-friendly name for the same set.
  - `History(ctx, d) []AppliedRecord` returns every applied
    migration newest-first as `{Version, Name, AppliedAt}`. Drives
    `/admin/migrations` endpoints — operators see the audit trail
    without reaching into psql.
  - `Generate(dir, name, opts...)` scaffolds a new migration file
    in dir. Default version stamping is "next NNNN" (scans dir,
    highest + 1, zero-padded to 4); `WithTimestamp()` flips to
    `YYYYMMDDHHMMSS` for shops where multiple devs land
    migrations independently; `WithDown()` also creates a matching
    `.down.sql` stub. Refuses to clobber existing files
    (`O_EXCL`-write). Name validation reuses the runner's
    filename alphabet.
  - New stable codes: `migrate_lock_failed`,
    `migrate_generate_invalid_name`, `migrate_generate_failed`.
- `db/jobs/` — four additive features. Existing Worker / Schedule /
  RegisterHandler signatures are preserved.
  - `WithDedupKey(key)` on Schedule makes the call idempotent
    against an already-queued row of the same type. Backed by a
    partial UNIQUE INDEX on (type, dedup_key) WHERE state='queued'.
    A second Schedule returns the existing row's ID instead of
    inserting. Cancelled/done/failed rows leave the partial index,
    so re-scheduling after completion always inserts cleanly.
  - `WithPriority(n)` on Schedule + new `priority integer` column.
    Claim SQL becomes `ORDER BY priority DESC, run_at`. The
    pending-rows partial index is rekeyed to
    `(queue, priority DESC, run_at)` to keep the hot path
    index-only. Defaults to 0 — back-compat for existing rows.
  - `Cancel(ctx, q, id)` operator helper: marks a queued row as
    `cancelled` (new state). Worker's claim SQL still filters on
    `state='queued'`, so cancelled rows are skipped. Returns
    `*errs.Error{KindNotFound, Code: jobs_not_found}` when the
    row is missing OR already in a non-queued state.
  - `GatherStats(ctx, q) Stats` returns
    `{Queued, Eligible, Running, Failed, Cancelled, Done,
    OldestQueued}` for /admin observability. One aggregate
    SELECT, partial-index covered.
  - `Worker.Shutdown(ctx) error` — deadline-aware sibling of
    `Stop()`. Signals shutdown the same way but returns
    `ctx.Err()` when in-flight handlers outlive the supplied
    deadline. Idempotent with subsequent Stop.
  - Schema: ADD COLUMN priority + dedup_key (both idempotent),
    DROP+CREATE pending partial index keyed on priority,
    CREATE UNIQUE INDEX idx_jobs_dedup_queued.
  - New stable codes: `jobs_not_found`, `jobs_op_failed`,
    `jobs_stats_failed`.
- `db/outbox/` — operator-helpers + observability + per-event-type
  policy. All additions are pure-add, no changes to existing
  Worker / Enqueue / Checker semantics.
  - `RetryNow(ctx, q, id)` forces an unpublished event to become
    eligible NOW (clears next_retry_at backoff). Loud-fails with
    `outbox_op_not_found` when the row is missing or already
    published — runbook scripts shouldn't silently no-op.
  - `Replay(ctx, q, ids...)` re-dispatches already-published rows
    by clearing published_at + attempts + last_error + stamping
    next_retry_at = NOW(). Returns the row count actually re-armed;
    missing IDs silently skipped (bulk operator action).
    Downstream consumers MUST be idempotent.
  - `ResetAttempts(ctx, q, id)` un-dead-letters a row that crossed
    WithMaxAttempts so the Worker fetches it again. Same
    not-found semantics as RetryNow.
  - `GatherStats(ctx, q) Stats` returns
    `{Pending, Eligible, Failed, OldestPending, Published1m}` —
    one aggregate SELECT for /admin observability.
  - `ListPending(ctx, q, limit)` / `ListDead(ctx, q, limit, maxAttempts)`
    surface top-N events by drain order / by attempts threshold,
    payload included, for queue-inspection endpoints.
  - `WithEventTypeMaxAttempts(map[string]int)` +
    `WithEventTypeBackoff(map[string]BackoffSpec)` — per-type
    overrides of the global retry cap / backoff window. When set,
    the drain SELECT drops the global attempts filter and per-type
    dead-letter decisions move into the Go dispatch loop. Rows
    already at their cap silently skip publishFn.
  - New stable error codes: `outbox_op_not_found`, `outbox_op_failed`,
    `outbox_stats_failed`, `outbox_list_failed`.
- `db/inbox/` — bulk dedupe + auxiliary helpers.
  - `ProcessBatch(ctx, db, keys, fn)` is the single-round-trip
    variant of Process: one INSERT ... UNNEST + ON CONFLICT DO
    NOTHING + RETURNING bulk-dedupes the slice, fn receives the
    indices of newly-inserted positions, the returned []Outcome
    aligns 1-to-1 with keys for atomic ack. Empty keys →
    `inbox_batch_empty`. Used for NATS pull-subscription handlers
    that pull 10–50 messages per fetch.
  - `Exists(ctx, q, key)` — pure check (no INSERT) for handlers
    that need to know "was this seen?" without recording.
  - `MarkProcessed(ctx, q, key)` — bare INSERT ON CONFLICT DO
    NOTHING that returns the Outcome. Use when the consumer
    side-effect already happened externally (third-party API
    confirmed delivery) and only the receipt needs recording.
- `db/sqb/` — three composable helpers for typed list endpoints:
  - `ParseSort` / `ApplySort` / `Sort` validate a comma-separated
    sort string (`"name,-created_at"`) against a caller-supplied
    `map[apiName]sqlColumn` allowlist and append `ORDER BY` clauses.
    Closes the SQL-injection gap documented on `Page` — list
    handlers no longer hand-roll the safelist. Unknown field →
    `*errs.Error{KindValidation, Code: sqb_invalid_sort}`.
  - `InBatches[T](items, size, fn)` chunks a slice and calls fn per
    chunk — for big `WHERE id IN (...)` under Postgres's
    65535-parameter bind cap, OR to bound row-lock holding times in
    bulk UPDATE/DELETE. Stops on first fn error; size ≤ 0 panics.
  - `CursorPage{Limit, After}` + `Cursor{CreatedAt, ID}` give
    base64-URL-safe keyset pagination over `(created_at, id)`
    tuples. Drops into `RegisterHandlerWithQuery` like `Page`.
    `Apply(b, "u.created_at", "u.id")` injects the keyset WHERE
    AND LIMIT clauses; columns are SQL-spliced and MUST be
    hardcoded by the handler. Bad cursor →
    `*errs.Error{KindValidation, Code: sqb_invalid_cursor}`.
- `db/lock/` — three additions around the advisory-lock primitive:
  - `WithLogger(*slog.Logger)` / `WithMetrics(prometheus.Registerer)`
    Options on `lock.New`. Logger records acquire / contended /
    released / error at the right slog levels. Metrics:
    `lock_acquires_total{name, outcome=acquired|contended|error}`
    counter + `lock_hold_duration_seconds{name}` histogram (1ms to
    10min buckets). One collector per Lock name — same convention
    as breaker/bulkhead. `RunOnce` / `RunBlocking` accept the same
    variadic Options.
  - `(*Lock).TryAcquireXact(ctx, *db.Tx) (bool, error)` —
    `pg_try_advisory_xact_lock` variant that auto-releases on tx
    commit/rollback, no manual release. Shares the namespace with
    session-level `TryAcquire`; nil tx → CodeAcquireFailed.
  - `(*Lock).IsHeld(ctx) (bool, error)` — diagnostic helper for
    /admin observability. TryAcquires + immediately releases; the
    result is a snapshot, not a control-flow primitive.
- `sentrykit/` — six additions around the existing bootstrap:
  process-wide Stats, per-fingerprint event rate limit,
  FiberMiddleware route filter, RecoverGo for background goroutines,
  AddBreadcrumb + SetUser helpers, and a default PII scrubber.
  - `GetStats() Stats` returns `{EventsCaptured, EventsDeduped,
    EventsRateLimited, BreadcrumbsEmitted, DedupeCacheSize}` —
    atomic counters for /admin observability of sentrykit itself.
  - `WithCaptureRateLimit(maxPerMin int)` adds a hard cap on Sentry
    events per fingerprint per minute. Composes with the existing
    window-based dedupe; suppressed events tick
    `Stats.EventsRateLimited`. Breadcrumbs still emit so downstream
    event timelines remain intact.
  - `FiberMiddlewareWithOptions(opts...)` is the option-driven
    variant of `FiberMiddleware`. `WithRouteFilter(fn)` skips hub
    cloning + scope populating for noisy paths;
    `DefaultRouteSkipFn` covers /healthz, /readyz, /metrics,
    /favicon.ico out of the box.
  - `RecoverGo(fn func())` wraps fn with recover + Sentry capture +
    Warn log. Canonical fire-and-forget goroutine pattern for code
    that can't tie a panic to a request lifecycle.
  - `AddBreadcrumb(ctx, category, message, data)` + `SetUser(ctx,
    id, email)` are short helpers over the hub resolved from ctx.
    No-op when no hub is configured.
  - `ScrubPII()` returns a BeforeSend hook that redacts
    Authorization / Cookie / X-API-Key / Set-Cookie headers and
    token-like query parameters (`token`, `secret`, `password`,
    `api_key`, `access_token`, `refresh_token`). `WithoutPII()` is
    the one-liner that installs it as the BeforeSend.
- `fibermap/` — seven opt-in middleware/RunOption additions covering
  common production HTTP needs: CORS, IP-keyed rate limit, body size
  cap, response compression, trusted proxies, slow-request level
  promotion, and a JSON-shape 404 catch-all.
  - `WithCORS(cfg...)` + `CORS(cfg...)` middleware wrapping
    `fiber/middleware/cors` with kit defaults (any origin, common
    methods/headers, 12h preflight cache).
  - `WithRateLimit(rps, burst, skipPaths...)` installs an in-process
    IP-keyed token-bucket. Skips `/healthz`, `/readyz`, `/metrics`
    by default. For multi-replica use the Redis-backed limiter via
    `WithUse`.
  - `WithBodyLimit(maxBytes)` sets `fiber.Config.BodyLimit` so the
    cap fires inside Fiber's parser BEFORE the body reaches the
    handler. `BodyLimit(n) fiber.Handler` is the standalone
    middleware for stricter subtree caps.
  - `WithCompression(level...)` installs gzip/deflate response
    compression. `Compression(level)` middleware exposed directly;
    `CompressionLevel` constants.
  - `WithTrustedProxies(cidrs...)` enables
    `fiber.Config.EnableTrustedProxyCheck` so `c.IP()` returns the
    `X-Forwarded-For` head only when the immediate hop is in the
    allowlist. Auto-sets `ProxyHeader` to `X-Forwarded-For`.
  - `RequestLoggerWithOptions(logger, opts...)` is the new
    option-driven access logger; `WithReqLogSlowThreshold(d)`
    promotes slow requests to Warn, demotes fast ones to Debug;
    5xx always stays Error. `WithReqLogSlowThresholdOption(d)` is
    the matching `RunOption`. `RequestLogger(logger, skipPaths...)`
    stays as the back-compat wrapper.
  - `WithNotFoundHandler(h)` installs a catch-all 404. Kit ships
    `NotFoundJSON()` for `{code: "not_found", message, path}`.
- `cronmap/` — six additions around the existing scheduler: per-job
  retry policy, lifecycle hooks, /admin-friendly Stats() snapshot,
  manual TriggerJob + NextRun prediction, and per-job Pause/Resume.
  - YAML `max_retries` + `retry_backoff` for per-job retry on err /
    timeout / panic. Backoff doubles per attempt, capped at base × 8.
    Successful retries surface as `success` outcome in metrics;
    exhausted retries fall through to `failure` / `timeout` based on
    the final attempt's error. Default = no retry (back-compat).
  - `WithOnTickStart(fn)` + `WithOnTickComplete(fn)` panic-safe
    lifecycle hooks fired before and after the (potentially retried)
    handler chain. Use for tracing span attrs, audit logs.
  - `Runtime.Stats() []JobStats` returns per-job snapshot:
    `{Name, Paused, TotalRuns, SuccessCount, FailureCount,
    TimeoutCount, SkippedCount, LastRunAt, LastOutcome,
    LastRunDuration, NextRunAt}`. atomic counters + mu only for
    last-run trio. Nil-receiver safe.
  - `Runtime.TriggerJob(ctx, name)` fires the named job
    synchronously, bypassing singleton lock and paused guard
    (operator /admin convention). Retry / hooks / metrics fire as
    on a normal tick. NotFound on unknown name. Works on a
    stopped runtime too (manual-only mode).
  - `Runtime.NextRun(name) (time.Time, error)` predicts the
    schedule's next fire time via `Schedule.Next(time.Now())`.
    NotFound on unknown name.
  - `Runtime.PauseJob(name) / ResumeJob(name)` toggle the
    scheduler-tick guard for individual jobs. Paused jobs accumulate
    `JobStats.SkippedCount` and Debug-log; TriggerJob ignores
    pause by design (operator override). Stable error code:
    `cronmap_unknown_job`.
- `batch/` — six production-quality additions around the existing
  Batcher. Panic recovery, backpressure cap, worker pool, retry
  policy, lifecycle hooks, and an /admin-friendly Stats() snapshot.
  - HandlerFn panics are recovered inside `runHandlerSafely` and
    surface as a regular error to the retry loop and ack callbacks.
    The flushLoop survives.
  - `Config.MaxPending` (default 0 = unbounded) caps the in-memory
    buffer. Submit drops the item and calls ack with
    `ErrPendingFull`; `TrySubmit(item, ack) error` returns the
    sentinel synchronously for callers needing immediate
    backpressure signal.
  - `Config.MaxInFlightHandlers` (default 1 = sequential —
    back-compat). When > 1, Flush spawns the dispatch into a
    goroutine; concurrent dispatches are bounded by a semaphore.
    Close waits for the in-flight dispatches.
  - `Config.MaxRetries` + `Config.RetryBackoffBase/Max`. Per-batch
    retry loop with exponential backoff; ack fires only after the
    final attempt.
  - `Config.OnBatchStart(ctx, size)` + `Config.OnBatchComplete(ctx,
    size, err, elapsed)` lifecycle hooks. Both panic-safe. Use for
    tracing span attrs and audit logging.
  - `Config.ContextFn func() context.Context` supplies the
    per-dispatch HandlerFn ctx (typically a tracing-aware ctx).
    Caller `Flush(ctx)` with non-Background ctx still wins.
  - `Batcher.Stats() Stats` returns `{Pending, InFlightHandlers,
    DispatchedTotal, FailedHandlers, RetriedAttempts}`. One mu
    acquire; nil-receiver safe.
- `breaker/` — five additions covering adaptive recovery, K-of-N
  half-open semantics, operator overrides, lifecycle hook, and an
  /admin-friendly snapshot.
  - `Config.OpenIntervalMultiplier` (default 1.0 = constant —
    back-compat) and `Config.OpenIntervalMax` (cap). Each
    consecutive trip without a successful close in between
    multiplies the effective open duration. Resets on close.
  - `Config.HalfOpenSuccessThreshold` (default = `HalfOpenMaxProbes`,
    i.e. legacy all-must-succeed) relaxes the half-open → closed
    transition to "K of N must succeed". A failure still rotates
    back to open regardless of running success count.
  - `Config.OnStateChange(from, to State)` is the panic-safe
    lifecycle hook fired inside the breaker mutex after every
    transition.
  - `Breaker.ForceOpen(d time.Duration)` jumps the breaker to open
    and pins it through the supplied window — operator override
    for maintenance or incident response. `Breaker.ForceClose()`
    is the manual reset.
  - `Breaker.Stats() Stats` returns `{State, Generation,
    WindowRequests, WindowFailures, HalfOpenInFlight,
    HalfOpenSucceeded, OpenedAt, RemainingOpen, ConsecutiveTrips,
    CurrentOpenInterval, ForcedOpenUntil}`. One mu acquire;
    nil-receiver safe.
- `bulkhead/` — two additions paralleling the breaker shape.
  - `Config.OnCapacityChange(prev, next int)` fires inside
    `SetCapacity` (manual or adaptive) on a non-trivial change.
    Panic-safe.
  - `Stats()` now exposes rolling `LatencyP50` / `LatencyP99` /
    `AvgWait` / `SampleSize` over `Config.StatsWindow` (default
    10s). Always-on (independent of `WithAdaptive`); backed by a
    bounded ring buffer (max 4096 entries) so /healthz reads stay
    cheap.
- `clients/webhooks/` — nine production-quality additions around the
  Worker. Outbound flow gets per-target circuit breakers, custom
  retry policy, lifecycle hooks, per-attempt timeout, panic
  recovery, TraceContext propagation, pluggable signer, override-able
  content-type, and a readiness Checker.
  - `WorkerConfig.BreakerFactory(subID) *breaker.Breaker` builds a
    per-subscription breaker on first sight and caches it in
    `sync.Map`. Open-state surfaces as retryable, rescheduling the
    delivery without burning the in-flight slot on a known-down
    endpoint.
  - `WorkerConfig.RetryClassifier(*http.Response, error) Outcome`
    overrides the kit's 2xx/408/429/5xx mapping. `Outcome` type +
    `OutcomeDelivered / OutcomeRetryable / OutcomeFatal` are
    exported; `DefaultClassifier` exposes the kit default as a
    building block.
  - `WorkerConfig.OnAttempt(d, resp, err, outcome, elapsed)` and
    `WorkerConfig.OnDLQ(d, status, errMsg)` are observability hooks.
    Both recover from user-callback panics.
  - `WorkerConfig.AttemptTimeout` replaces the hardcoded 30s.
  - `WorkerConfig.SignerFunc(body, secret, now) (string, error)`
    swaps the Stripe-style HMAC signature for any app-specific
    scheme.
  - `WorkerConfig.DefaultContentType` overrides hardcoded
    `application/json`. `Delivery.Headers["Content-Type"]` still
    wins on a per-call basis.
  - `WorkerConfig.Propagator propagation.TextMapPropagator` injects
    W3C TraceContext (`traceparent` / `tracestate`) onto outbound
    headers. Same shape as `clients/nats` publish-side propagation.
  - Panic recovery in the attempt goroutine — slot released,
    delivery rescheduled via `fail()`, logged at Warn.
  - `NewChecker(store, name) *Checker` is the /readyz adapter —
    pings `DeliveryStore.Claim(ctx, 0)`. Same shape as
    `clients/nats.Checker` + `clients/redis.Checker`.
- `clients/cache/` — five additions: UniversalClient adapter, owned
  metrics, read-through helper with single-flight, TTL jitter,
  pluggable codec, and prefix-scoped invalidation.
  - `New[T]` now accepts `redis.UniversalClient` so the same cache
    type works against single-node, cluster, and sentinel
    deployments. `For[T]` threads through `rc.Universal()`. Same
    surface for the in-package `RedisIdempotencyStore`.
  - `Config.Name` + `Config.MetricsReg` register
    `cache_operations_total{name,operation,outcome}` and
    `cache_operation_duration_seconds{name,operation}`. Name is
    required when MetricsReg is set. Operations: `get` /
    `set` / `set_not_found` / `invalidate` / `invalidate_prefix`.
    Outcomes: `hit` / `miss` / `negative` / `ok` / `error`.
  - `(*Redis[T]).GetOrLoad(ctx, key, LoaderFn[T])` is the
    read-through helper. `LoaderFn[T] = func(ctx, key) (T, bool,
    error)` — `(val, true, nil)` → positive cache; `(zero, false,
    nil)` → SetNotFound (negative cache); `(zero, false, err)` →
    surface to caller without poisoning the cache. Internal
    `singleflight.Group` collapses concurrent requests on the same
    key into one loader invocation.
  - `Config.TTLJitter` applies ±fraction uniform noise to the
    effective PositiveTTL / NegativeTTL on every Set call.
    Defaults to 0 (no jitter); typical production values 0.10 -
    0.25. Defends popular keys against synchronised expiry storms.
  - `Codec` interface + `JSONCodec` default + `Config.Codec`
    override. Plug msgpack / protobuf / custom shapes without
    forking the cache.
  - `(*Redis[T]).InvalidatePrefix(ctx, partial)` walks the
    keyspace via SCAN + pipelined DEL — bounded round-trips, no
    KEYS. Best-effort error policy. Cluster-mode caveat: SCAN runs
    per-shard; pin tenant keys via hashtag for full coverage.
- `clients/redis/` — five additions covering production topologies,
  hook composability, status observability, and resilience.
  - `ConnectCluster(ctx, ClusterConfig, opts...)` (cluster mode via
    `redis.NewClusterClient`) + `ConnectSentinel(ctx, SentinelConfig,
    opts...)` (HA failover via `redis.NewFailoverClient`). `Client`
    now wraps `redis.UniversalClient` internally; `Client.Universal()`
    is the cross-mode escape hatch; `Client.Redis()` stays for
    single-mode back-compat (returns nil under cluster/sentinel);
    `Client.Mode()` reports the topology. Observability, breaker,
    and default-timeout options work identically across modes.
  - `WithClusterOptions(func(*redis.ClusterOptions))` /
    `WithSentinelOptions(func(*redis.FailoverOptions))` are the
    cluster/sentinel mutators paralleling `WithRedisOptions` for
    single mode.
  - `WithHook(redis.Hook)` appends user hooks AFTER the kit
    observability hook. Multiple calls accumulate.
  - `WithDefaultTimeout(d time.Duration)` wraps every command's ctx
    via `context.WithTimeout(d)` when the caller has not already
    set a deadline. Caller deadlines pass through unchanged.
  - `WithBreaker(*breaker.Breaker)` routes every command through
    `breaker.Execute`. `redis.Nil` is treated as success (the
    "key not found" signal must not trip the breaker). Open-state
    surfaces as `*errs.Error{KindUnavailable, Code:
    "redis_circuit_open"}` wrapping `breaker.ErrOpen`.
  - `redis_connection_status` gauge added under `WithMetrics` —
    symmetric with `nats_connection_status`. Flips to 1 after a
    successful Connect ping; flips to 0 in `Close`.
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


---

*Released v0.x sections archived to [`docs/CHANGELOG-0.x.md`](docs/CHANGELOG-0.x.md) at the v1 freeze. The main file keeps `[Unreleased]` + every section since the most recent release.*
