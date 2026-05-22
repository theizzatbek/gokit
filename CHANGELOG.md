# Changelog

All notable changes to fibermap. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versioning is
0.x — every minor bump may include breaking changes until a 1.0 tag.

This is the bootstrap entry; prior history lives in `git log`.

## [Unreleased]

### Added
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
  (MustCompile convention).
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