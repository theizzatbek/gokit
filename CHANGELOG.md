# Changelog

All notable changes to fibermap. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versioning is
0.x — every minor bump may include breaking changes until a 1.0 tag.

This is the bootstrap entry; prior history lives in `git log`.

## [Unreleased]

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