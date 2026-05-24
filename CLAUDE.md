# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

A single-package Go library (`github.com/theizzatbek/fibermap`, Go 1.23) that loads a YAML-described HTTP route tree onto a [Fiber v2](https://github.com/gofiber/fiber) router. No `cmd/`, no subpackages — everything lives in `package fibermap` at the repo root. Tests use only the standard library plus Fiber's in-process test helpers.

## Commands

```bash
go test ./...                      # full suite
go test -run TestEngine_Mount ./.  # one test by name
go test -race -count=1 ./...       # race-checked, no cache
go vet ./...
gofmt -l .                         # check formatting (CI-style)
```

There is no Makefile, no linter config, and no test fixtures outside `testdata/*.yaml`.

## Architecture — what spans multiple files

The whole library is a build-once-mount-once configurator (`Engine[T]`) parameterized by the per-request payload type `T`. Understanding the system means understanding three things that span files:

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
- Root helper `fibermap.ErrorHandler(logger *slog.Logger) fiber.ErrorHandler` (in `error_handler.go`) wires `errs.HTTP` into Fiber and falls back to `*fiber.Error`'s own code for router-level errors (404/405). Auto-logs 5xx responses via the passed logger; 4xx is silent by default. Pass `nil` logger to use `slog.Default()`.

## YAML shape

Defined by the unexported structs in `spec.go` (`rawConfig`, `rawGroup`, `rawRoute`, `mwRef`). Groups nest. Both groups and routes accept a single `middleware_set:` name plus an explicit `middleware:` list — `combineSetAndList` prepends the set name and `resolveChain` expands it. `middleware:` items are heterogeneous: scalar string → `mwRef{Name}` (plain), single-key map `{name: [args...]}` → `mwRef{Name, Args}` (factory). Decoded by `mwRef.UnmarshalYAML` in yaml.go. Only the methods in `validHTTPMethods` (yaml.go) are accepted. `testdata/*.yaml` covers the supported shapes (nested groups, sets, factories, duplicate-route detection, cycle detection).

## Status

README declares `0.x — API unstable`. Breaking changes to exported types are acceptable; do not add backwards-compat shims unless asked.