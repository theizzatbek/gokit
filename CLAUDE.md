# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

A composable Go service kit (`github.com/theizzatbek/gokit`, Go 1.23+) — eight independently importable packages that cover routing, errors, database, auth, outbound HTTP, declarative outbound APIs, and NATS event streaming. Each subpackage lives under the umbrella module path; root `gokit` package itself has no exported symbols — it exists only as the module path. Tests use stdlib + each subpackage's specific helpers (testcontainers for `db`/`auth/refreshpg`/`auth/refreshredis`/`clients/nats`; in-process Fiber test helpers for `fibermap`).

The YAML-declarative router that originally gave the repo its name now lives at `fibermap/` as one of the eight peers. Most of the "Architecture" notes below describe that subpackage specifically — those patterns are not necessarily mirrored in `errs`, `db`, `auth`, or `clients/*` (each has its own design spec under `docs/superpowers/specs/`).

## Commands

Run from repo root — `go test ./...` covers every subpackage.

```bash
go test ./...                      # full suite
go test -run TestEngine_Mount ./fibermap  # one test by name
go test -race -count=1 ./...       # race-checked, no cache
go vet ./...
gofmt -l .                         # check formatting (CI-style)
```

There is no Makefile and no project-wide linter config.

## Architecture — `fibermap/` subpackage internals

The notes in this section describe the `gokit/fibermap` router (the YAML build-once-mount-once configurator). Same patterns are not necessarily mirrored in other subpackages — see their respective bullets at the bottom of the file.

The whole router is a build-once-mount-once configurator (`Engine[T]`) parameterized by the per-request payload type `T`. Understanding the system means understanding three things that span files:

### 1. The lifecycle is strict and enforced at Mount time

`doc.go` documents it: `New → SetContextBuilder → RegisterHandler / RegisterMiddleware / RegisterMiddlewareFactory → LoadFile/LoadBytes → Mount`. `Mount` validates everything together and returns an `errors.Join` of all problems — it does not install any route if validation produces even one error. `Mount` may only be called once per engine (`CodeAlreadyMounted`). Adding new constraints belongs in `Engine.buildPlan` (engine.go), which already accumulates `*Error` values into a slice.

### 2. The per-request `Context[T]` is built exactly once and propagated through Fiber's `Locals`

`installPlan` (engine.go) installs a single root middleware (`contextInit`) via `router.Use` that calls `e.builder`, wraps the result in `&Context[T]{Ctx: c, Data: data}`, and stores it under the unexported `ctxKey` constant. Every per-route wrapper (`wrapMW`, `wrapHandler`) reads it back from `Locals`. If the cast fails, both wrappers return **500** rather than silently bypassing — this is intentional (see commit `74c6569`). Any new wrapper added to the chain must follow the same "missing context = 500" convention.

### 3. Middleware-chain resolution lives in `chain.go`

`resolveChain` flattens: outermost-ancestor groups first, then route-level middleware. Each entry is an `mwRef` (`{Name, Args}`); plain middleware has nil Args, factory middleware carries the YAML args. Sets named in `middleware_sets` are recursively expanded; duplicates are deduped by `(Name, Args)` via `dedupKey` — same factory with different args coexists in the chain.

At `installPlan` time the engine builds a `dedupKey → fiber.Handler` cache from registered factories. Plain middleware (`ref.Args == nil`) bypasses the cache; factory middleware (any non-nil Args, even empty) calls `e.factories[Name](args)` once per unique `dedupKey` and caches the result. A factory returning an error aborts mount with `CodeInvalidFactoryArgs`.

The plain/factory split is enforced at `buildPlan` time: a YAML scalar referencing a factory name (or a YAML map referencing a plain name) surfaces as `CodeUnknownMiddleware` with a guiding message rather than silently invoking the wrong code path.

### Errors are typed, not strings

Every error returned by the library is `*Error` (errors.go) with `Stage` (`parse` / `mount` / `register`) and a `Code*` constant. New error conditions should add a `Code*` constant and use `*Error`, never `fmt.Errorf`. Parse-stage errors come from `yaml.go` (`parseBytes`, `validateGroups`, `detectSetCycles`); mount-stage errors are appended to the `errs` slice inside `buildPlan` so multiple problems surface in one `Mount` call.

Register stage is the one exception that does **not** return an error: `Register{Handler,Middleware,MiddlewareFactory}` panic with `*Error` on duplicate-name conflicts (within or across the plain/factory registries). This is intentional — duplicate registration is a programmer error at startup and the `MustCompile` convention keeps call sites uncluttered. Tests that exercise this use `defer recover()` (see `expectRegisterPanic` in engine_test.go).

### Subpackages

- `errs/` — typed domain errors + HTTP mapping. `*errs.Error{Kind, Code, Message, Details, Cause}` carries a closed-enum `Kind`, a stable string `Code`, optional `Details` for field-level failures, and the wrapped `Cause`. Per-`Kind` constructors (`errs.NotFound`, `errs.Validation`, …) and `…f` Sprintf variants. `errs.Wrap(err, kind, code, msg)` lifts an underlying error. `errs.HTTP(err) (status, body)` produces the wire shape `{code, message, details?}`. `*Error` implements `slog.LogValuer` so `logger.Error("...", "err", e)` emits structured attrs. Stdlib-only — no Fiber, no validator deps.
- `errs/errsval/` — converts `validator.ValidationErrors` into `*errs.Error` of `KindValidation` with populated `Details`. Depends on `go-playground/validator/v10`. Kept in a subpackage so `errs/` stays stdlib-only.
- `db/` — pgx-based pool wrapper. `*db.DB` from `db.Connect(ctx, cfg, opts...)` exposes `Query/QueryRow/Exec` (errors mapped to `*errs.Error` via `mapPgxErr`), `Tx(ctx, fn)` for functional transactions (nested calls open savepoints via `pgx.Tx.Begin`), `Healthcheck`, and a `Pool()` escape hatch. Observability is opt-in via `WithLogger(*slog.Logger)`, `WithMetrics(prometheus.Registerer)`, `WithSlowQueryThreshold(time.Duration)`. No fiber dep. Tests use `testcontainers-go/modules/postgres` — Docker required.
- `db/sqb/` — opt-in squirrel wrapper preconfigured for `$N` placeholders. `sqb.Builder` for query construction, `sqb.Query`/`sqb.Exec` to run builders against any `db.Querier`. Depends on `db/` (one-way; core `db/` does NOT import `sqb`).
- `auth/` — JWT issue/verify (asymmetric EdDSA/ES256), generic `Claims[C]`, argon2id `Hasher`, refresh-token interface, and ready-to-mount middleware (Bearer/RequireScope/RequireRole) + handlers (Login/Refresh/Logout). `auth.New[C](Config, ...Option) *Auth[C]` is the entry point. Core package depends on stdlib + crypto + `golang-jwt/jwt/v5` + fiber + `errs/`; deliberately does NOT import `db/` or `redis`. Refresh persistence is pluggable via the `RefreshStore` interface. Access tokens carry `kid` in the header for rotation; `KeySet.LoadKeysFromPEM` accepts a mix of private/public PEMs so verify-only services can be wired without signing material.
- `auth/refreshpg/` — Postgres-backed `RefreshStore` over `db.Querier`. DDL lives in `auth/refreshpg/schema.sql`; the package does not run migrations. Atomic `Consume` is one `UPDATE ... RETURNING` followed by a diagnostic `SELECT` on the miss path; reuse detection triggers a family-wide `RevokeFamily` before returning the `refresh_reused` error.
- `auth/refreshredis/` — Redis-backed `RefreshStore` over `redis/go-redis/v9`. Each record is one HASH with `EXPIREAT`; family + subject SETs back the bulk-revoke paths. `Consume` runs as a single Lua script for atomicity (`Consume + reuse detection + family revoke` all server-side).
- `auth/fibermount/` — wires an `auth.Auth[C]`'s middleware factories into a `*fibermap.Engine[T]` in one call (`fibermount.MountMiddlewareFactories(eng, a)`). The bridge lives in a subpackage so core `auth/` stays free of any `fibermap` import.
- `clients/nats/` — typed NATS / JetStream client wrapper. `natsclient.Connect(ctx, Config) (*Client, error)` opens a connection + JetStream context. Generic `Publisher[T]` / `Subscribe[T]` over an opt-in `Codec` (JSON default). Auto-ack handler model: handler returns nil → Ack, err → Nak with exponential backoff, decode-fail → Term (poison pill). `MaxInFlight` semaphore caps handler concurrency. Idempotent `EnsureStream(ctx, StreamConfig)` for app-managed stream lifecycle. Errors map to `*errs.Error` with stable Code constants (`connect_failed`, `stream_not_found`, `publish_failed`, …). Opt-in slog/Prometheus observability via `WithLogger` / `WithMetrics`. Replaces the kit-overview slot originally allocated to `jobs/`.
- `clients/httpc/` — outbound HTTP client builder. `httpc.New(Config, ...Option) (*http.Client, error)` returns a stdlib `*http.Client` whose transport chain wraps the user-supplied or `http.DefaultTransport` with per-attempt `context.WithTimeout`, full-jitter exponential retry on transient failures (5xx, 429, 408, network errors — idempotent methods only; POST/PATCH never retry), and opt-in slog/Prometheus observability. `NewTransport(cfg, opts...)` returns the same chain unwrapped for users composing into their own client (otel, auth middleware). Errors from validation map to `*errs.Error` (Codes: `httpc_invalid_timeout`, `httpc_invalid_max_retries`, `httpc_invalid_backoff`); runtime network errors and exhausted-retry responses pass through unchanged — drop-in stdlib semantics. `Retry-After` honoured (capped at `4 * BackoffMax`); body replay via `req.GetBody` (stdlib convention). `MaxRetries: -1` disables retries entirely; the zero value gets the default (3). Depends only on `errs/` and `prometheus/client_golang`; no fiber, no fasthttp.
- `clients/apimap/` — declarative outbound HTTP layer. Upstream APIs are described in YAML (clients, endpoints, methods, paths, encoding/decoding, per-endpoint timeout/retry overrides, per-client auth); the engine validates everything at `Build` and returns a goroutine-safe `*Client` keyed by `<client>.<endpoint>`. Auth shapes (`type: basic|bearer|header|none`) resolve to a single header applied per-request; secrets come from `${ENV_VAR}` substituted into the YAML at Load. `apimap.Decode[T](ctx, c, name, Call) (T, error)` decodes 2xx response bodies; non-2xx maps to `*errs.Error` with `Kind` derived from status and a stable per-endpoint `Code` (e.g. `apimap_github_get_user_not_found`). Response context (status / url / truncated body) sits in `*errs.Error.Details`. `Exchange[Req, Resp]` adds typed request encoding. `Do(ctx, name, Call)` is the stdlib-style escape hatch returning `*http.Response` unwrapped. Transport is `clients/httpc.New(...)` — one `*http.Client` per upstream by default, dedicated `*http.Client` per endpoint when any `timeout`/`max_retries`/`backoff_*` is overridden. Encoding modes: `json`/`form`/`raw`/`none`. Decoding modes: `json`/`raw`/`none`. Depends on `errs/`, `clients/httpc`, and `gopkg.in/yaml.v3`; no fiber, no fasthttp, no `db`/`auth`/`nats`.
- `fibermap.ErrorHandler(logger *slog.Logger) fiber.ErrorHandler` (in `fibermap/error_handler.go`) wires `errs.HTTP` into Fiber and falls back to `*fiber.Error`'s own code for router-level errors (404/405). Auto-logs 5xx responses via the passed logger; 4xx is silent by default. Pass `nil` logger to use `slog.Default()`.

## YAML shape

Defined by the unexported structs in `fibermap/spec.go` (`rawConfig`, `rawGroup`, `rawRoute`, `mwRef`). Groups nest. Both groups and routes accept a single `middleware_set:` name plus an explicit `middleware:` list — `combineSetAndList` prepends the set name and `resolveChain` expands it. `middleware:` items are heterogeneous: scalar string → `mwRef{Name}` (plain), single-key map `{name: [args...]}` → `mwRef{Name, Args}` (factory). Decoded by `mwRef.UnmarshalYAML` in `fibermap/yaml.go`. Only the methods in `validHTTPMethods` (`fibermap/yaml.go`) are accepted. `fibermap/testdata/*.yaml` covers the supported shapes (nested groups, sets, factories, duplicate-route detection, cycle detection).

## Status

README declares `0.x — API unstable`. Breaking changes to exported types are acceptable; do not add backwards-compat shims unless asked.