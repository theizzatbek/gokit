# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

A composable Go service kit (`github.com/theizzatbek/gokit`, Go 1.23+) — eight independently importable packages that cover routing, errors, database, auth, outbound HTTP, declarative outbound APIs, and NATS event streaming. Each subpackage lives under the umbrella module path; root `gokit` package itself has no exported symbols — it exists only as the module path. Tests use stdlib + each subpackage's specific helpers (testcontainers for `db`/`auth/refreshpg`/`auth/refreshredis`/`clients/nats`; in-process Fiber test helpers for `fibermap`).

The YAML-declarative router that originally gave the repo its name now lives at `fibermap/` as one of the eight peers. The "Architecture" section below describes that subpackage specifically — those patterns are not necessarily mirrored in `errs`, `db`, `auth`, or `clients/*` (each has its own design spec under `docs/superpowers/specs/` and a kit-overview page under `docs/packages/`).

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

The notes in this section describe the `gokit/fibermap` router (the YAML build-once-mount-once configurator). Same patterns are not necessarily mirrored in other subpackages — see `docs/packages/<area>.md` for those.

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

## Subpackages

Each entry is a one-liner: what it is + где смотреть полное описание. Полные API-спеки (контракты, env-vars, observability) живут в `docs/packages/`.

- `errs/`, `errs/errsval/` — типизированные ошибки + HTTP-маппинг → [docs/packages/errs.md](docs/packages/errs.md)
- `db/`, `db/sqb/`, `db/testdb/` — pgx-pool, мульти-реплики, testcontainers → [docs/packages/db.md](docs/packages/db.md)
- `auth/`, `auth/refreshpg/`, `auth/refreshredis/`, `auth/apikeypg/`, `auth/fibermount/`, `auth/sessions/`, `auth/sessionsredis/` — JWT, sessions, refresh, API keys → [docs/packages/auth.md](docs/packages/auth.md)
- `clients/nats/`, `clients/httpc/`, `clients/apimap/`, `clients/natsmap/`, `clients/redis/`, `clients/cache/`, `clients/webhooks/` — outbound integrations → [docs/packages/clients.md](docs/packages/clients.md)
- `breaker/`, `bulkhead/`, `batch/` — resilience-примитивы → [docs/packages/resilience.md](docs/packages/resilience.md)
- `cronmap/` — декларативный cron поверх YAML → [docs/packages/cronmap.md](docs/packages/cronmap.md)
- `fibermap/sse/`, `fibermap/ws/`, `fibermap/wsnats/`, `fibermap.ErrorHandler` — стримы и WebSocket поверх router'a → [docs/packages/fibermap-extras.md](docs/packages/fibermap-extras.md)
- `sentrykit/` — Sentry bootstrap + Fiber middleware + slog breadcrumbs → [docs/packages/sentrykit.md](docs/packages/sentrykit.md)
- `service/` — all-in-one bootstrap (бандл всего вышеперечисленного) → [docs/packages/service.md](docs/packages/service.md)

## YAML shape

Defined by the unexported structs in `fibermap/spec.go` (`rawConfig`, `rawGroup`, `rawRoute`, `mwRef`). Groups nest. Both groups and routes accept a single `middleware_set:` name plus an explicit `middleware:` list — `combineSetAndList` prepends the set name and `resolveChain` expands it. `middleware:` items are heterogeneous: scalar string → `mwRef{Name}` (plain), single-key map `{name: [args...]}` → `mwRef{Name, Args}` (factory). Decoded by `mwRef.UnmarshalYAML` in `fibermap/yaml.go`. Only the methods in `validHTTPMethods` (`fibermap/yaml.go`) are accepted. `fibermap/testdata/*.yaml` covers the supported shapes (nested groups, sets, factories, duplicate-route detection, cycle detection).

## Status

README declares `0.x — API unstable`. Breaking changes to exported types are acceptable; do not add backwards-compat shims unless asked.
