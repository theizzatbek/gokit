# Contributing to gokit

Thanks for taking the time to contribute. This is a small kit and the bar
for additions is high — we'd rather keep the surface small than ship
features we can't maintain.

## Before you open a PR

- **Discuss large changes first.** Open an issue describing the use case
  and the API shape you have in mind. New subpackages, new YAML keys, and
  anything that changes wire formats (metric labels, error codes, JSON
  shapes) need a quick check before you spend time on code.
- **Bug fixes don't need a pre-discussion.** If you found a bug and can
  fix it, open the PR.
- **Read the package's README and `doc.go`.** Most "should we add X?"
  questions are answered there ("kit is stdlib-only", "errors return
  `*errs.Error`, never `fmt.Errorf`", "factory args bypass the cache",
  etc.).

## Local development

```bash
git clone https://github.com/theizzatbek/gokit
cd gokit

# Unit tests only — no Docker required, ~5s.
go test -short ./...

# Full suite — needs Docker (testcontainers). ~2 min on a warm cache.
go test ./...

# Race detector on unit code.
go test -race -short ./...

# Static checks (same as CI).
go vet ./...
gofmt -l .   # must be empty
```

Required: **Go 1.26+** and a running Docker daemon for integration tests.

## Commit messages

Follow [Conventional Commits](https://www.conventionalcommits.org/):

```
<type>(<optional scope>): <subject>

<optional body explaining why, not what>
```

Common types in this repo: `feat`, `fix`, `chore`, `docs`, `ci`,
`refactor`. The scope is usually the subpackage:

```
fix(testdb): per-replica handle must connect with target_session_attrs=standby
feat(auth): WithIPExtractor for CDN-aware refresh-token IP capture
docs: split CLAUDE.md into per-area pages under docs/packages/
```

Multi-line bodies are encouraged for the **why**. Don't restate the
diff — restate the constraint, prior incident, or design tradeoff
that led to the change.

## Code conventions

- **Errors are `*errs.Error`, not `fmt.Errorf`.** Every error returned
  from an exported function should be `errs.Wrap(...)` or one of the
  `errs.*` constructors. New error conditions get a new `Code*` constant.
- **No `panic` for runtime conditions.** `panic` is reserved for
  programmer errors at startup (duplicate handler registration, etc.) —
  same convention `fibermap.Register*` follows.
- **Observability is opt-in.** `WithLogger` / `WithMetrics` options;
  default is silent. Metric names use snake_case with the package as
  prefix (`db_query_duration_seconds`, `auth_apikey_authentications_total`).
- **No `init()` functions outside `cmd/`.** Subpackages should be
  side-effect-free at import time.
- **Use the dedicated tools, not the shell**: `Edit` not `sed`, `Read`
  not `cat`. (Applies to humans too — keep the diff visible in PRs.)

## Adding tests

- Integration tests that need Docker should call `testdb.Spin(t)` /
  `testcontainers.Run(...)` directly. The skip-under-`-short` pattern
  (`if testing.Short() { t.Skip(...) }`) is required so unit-only runs
  finish fast.
- For multi-component fixtures (e.g. Postgres + NATS), prefer
  `testcontainers-go/modules/*` over manual container management.
- Race-prone code (anything with a goroutine) needs at least one
  `go test -race` test exercising the concurrent path.

## PR flow

1. Branch off `main` with a descriptive name:
   `feat/<area>-<short>`, `fix/<area>-<short>`, `chore/<short>`.
2. Make focused changes — one logical concern per PR. Split unrelated
   cleanups into separate PRs even if they're trivial.
3. Push and open a PR. CI runs `gofmt -l`, `go vet ./...`, unit-race,
   and full integration. All three must pass.
4. If your PR adds a new subpackage or YAML key, also update:
   - The package's own `README.md` and `doc.go`.
   - The top-level [`README.md`](README.md) decision table where
     relevant.
   - [`CHANGELOG.md`](CHANGELOG.md) under `## [Unreleased]`.

## Versioning

`gokit` is currently `0.x` — every minor bump may include breaking
changes. After `v1.0.0`, semver applies strictly. See
[`docs/versioning.md`](docs/versioning.md) for what counts as a
breaking change (covers exported symbols, metric labels, error codes,
YAML shape).

## Reporting security issues

Don't open a public issue. See [`SECURITY.md`](SECURITY.md) for the
private disclosure path.
