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

`doc.go` documents it: `New → SetContextBuilder → RegisterHandler/RegisterMiddleware → SetRoleChecker (if YAML uses roles) → LoadFile/LoadBytes → Mount`. `Mount` validates everything together and returns an `errors.Join` of all problems — it does not install any route if validation produces even one error. `Mount` may only be called once per engine (`CodeAlreadyMounted`). Adding new constraints belongs in `Engine.buildPlan` (engine.go), which already accumulates `*Error` values into a slice.

### 2. The per-request `Context[T]` is built exactly once and propagated through Fiber's `Locals`

`installPlan` (engine.go) installs a single root middleware (`contextInit`) via `router.Use` that calls `e.builder`, wraps the result in `&Context[T]{Ctx: c, Data: data}`, and stores it under the unexported `ctxKey` constant. Every per-route wrapper (`wrapMW`, `wrapHandler`, `wrapRoleGuard`) reads it back from `Locals`. If the cast fails, all three wrappers return **500** rather than silently bypassing — this is intentional (see commit `74c6569`). Any new wrapper added to the chain must follow the same "missing context = 500" convention.

### 3. Middleware-chain resolution lives in `chain.go` and uses a sentinel

`resolveChain` flattens: outermost-ancestor groups first, then route-level middleware, then — if the route has `roles:` — the sentinel name `roleGuardName` (`"__role_guard__"`). `engine.installPlan` looks for that sentinel and substitutes `wrapRoleGuard(route.Roles)`; everything else looks up `e.middlewares[name]`. Sets named in `middleware_sets` are recursively expanded inside `resolveChain`; duplicate names are deduped keeping first occurrence. `Engine.Routes()` returns introspection records with the sentinel filtered out (`filterOutSentinel`).

### Errors are typed, not strings

Every error returned by the library is `*Error` (errors.go) with `Stage` (`parse` / `mount` / `register`) and a `Code*` constant. New error conditions should add a `Code*` constant and use `*Error`, never `fmt.Errorf`. Parse-stage errors come from `yaml.go` (`parseBytes`, `validateGroups`, `detectSetCycles`); mount-stage errors are appended to the `errs` slice inside `buildPlan` so multiple problems surface in one `Mount` call.

## YAML shape

Defined by the unexported structs in `spec.go` (`rawConfig`, `rawGroup`, `rawRoute`). Groups nest. Both groups and routes accept a single `middleware_set:` name plus an explicit `middleware:` list — `combineSetAndList` prepends the set name and `resolveChain` expands it. Only the methods in `validHTTPMethods` (yaml.go) are accepted. `testdata/*.yaml` covers the supported shapes (nested groups, sets, roles, duplicate-route detection, cycle detection).

## Status

README declares `0.x — API unstable`. Breaking changes to exported types are acceptable; do not add backwards-compat shims unless asked.