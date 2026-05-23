# tasks — realistic fibermap example

A small per-user tasks/CRUD API meant as a **starting template**, not a
teaching demo. Copy this directory, rename, adjust, ship.

## What's actually in here (and why)

| Path | Why |
| --- | --- |
| `main.go` | wire-up via `eng.Run(...)` — one call covers `fiber.New(custom config)`, `app.Use(request_id, auth.Bearer)`, `LoadFS` of embedded `routes.yaml`, `Mount`, `Listen(":3000")`, and SIGINT/SIGTERM graceful shutdown |
| `routes.yaml` | declarative route tree, mounted via `Engine.LoadFS`; modeline at the top gives editor autocomplete via JSON Schema |
| `internal/appctx/` | `AppCtx` struct (user_id, role, request_id, scoped logger) + `Ctx` / `H` / `MW` aliases so handler signatures don't carry the generic parameter |
| `internal/config/` | env-driven `Config` struct via `caarlos0/env/v11` — `ADDR`, `LOG_LEVEL`, `CORS_ORIGINS`, etc. See `.env.example` |
| `internal/auth/` | Fiber-level Bearer-token middleware (runs **before** `ContextBuilder`) + fibermap factory `require_role` |
| `internal/tasks/` | domain — `Task` model, `Store` interface (memory impl behind it; swap for postgres without touching handlers), handlers using `bind.Body[T]` with `go-playground/validator` tags |
| `internal/admin/` | `/admin/routes` endpoint built on `Engine.Routes()` — handy ops endpoint, also shows the JSON tags on `RouteInfo` in action |
| `main_test.go` | `fibermaptest.AssertRoute` for "routes.yaml says what we think" + end-to-end requests through `fiber.App.Test()` for "the whole stack actually responds" |

## Try it

```bash
go run ./examples/tasks
```

Demo tokens are baked in (`internal/auth/auth.go`):
- `alice-token`, `bob-token` — `role=user`
- `root-token` — `role=admin`

```bash
# unauthenticated → 401
curl -i http://localhost:3000/api/v1/tasks

# alice creates a task → 201
curl -i -H "Authorization: Bearer alice-token" \
        -H "Content-Type: application/json" \
        -d '{"title":"buy milk"}' \
        http://localhost:3000/api/v1/tasks

# alice lists her tasks → 200 + JSON
curl -i -H "Authorization: Bearer alice-token" \
        http://localhost:3000/api/v1/tasks

# alice tries to delete → 403 (require_role: [admin])
curl -i -X DELETE -H "Authorization: Bearer alice-token" \
        http://localhost:3000/api/v1/tasks/$TASK_ID

# root (admin) deletes → 204
curl -i -X DELETE -H "Authorization: Bearer root-token" \
        http://localhost:3000/api/v1/tasks/$TASK_ID

# admin lists every route fibermap registered → 200 + JSON
curl -i -H "Authorization: Bearer root-token" \
        http://localhost:3000/api/v1/admin/routes
```

## Configuration

Everything is env-driven via `internal/config`. The shipped defaults
match what you see in `curl` examples above — listens on `:3000`,
JSON logs at `info`, open CORS, 100 req/min per IP. Override any field
by exporting the corresponding env var (or dropping a `.env` next to
the binary and sourcing it).

| Var | Default | Meaning |
| --- | --- | --- |
| `ADDR` | _(unset)_ → `$PORT` / `:3000` | Listen address. When unset, `fibermap.Run` honors `$PORT` (cloud convention) and falls back to `:3000`. Set `ADDR` explicitly to override both |
| `SHUTDOWN_TIMEOUT` | `10s` | Graceful drain budget on SIGINT/SIGTERM |
| `BODY_LIMIT` | `1048576` (1 MiB) | `fiber.Config.BodyLimit` |
| `LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error` |
| `LOG_FORMAT` | `json` | `json` / `text` |
| `CORS_ORIGINS` | `*` | Comma-separated origins for `cors.AllowOrigins` |
| `CORS_METHODS` | `GET,POST,PATCH,DELETE,OPTIONS` | `cors.AllowMethods` |
| `RATE_LIMIT_MAX` | `100` | Requests per window per IP |
| `RATE_LIMIT_EXPIRATION` | `1m` | Window length |
| `ENV` | `development` | `development` / `staging` / `production` |
| `API_BASE_URL` | _(unset)_ | If set, used as OpenAPI `servers[0].url` |

Full template: [`.env.example`](.env.example).

## Patterns you'd want to copy

1. **`AppCtx` carries everything request-scoped.** `UserID`, `Role`,
   `RequestID`, and a `*slog.Logger` pre-bound with both. Handlers do
   `c.Data.Log.Info("created", ...)` and lines automatically correlate
   to the user and request.

2. **Auth at the Fiber level, authorization at the fibermap level.**
   `Bearer()` runs via `app.Use(...)` *before* fibermap's
   `ContextBuilder` so it can set the locals the builder reads.
   `require_role` runs as a fibermap chain entry so it's visible in
   `routes.yaml` and easy to introspect/test.

3. **`embed.FS` for `routes.yaml`.** `//go:embed routes.yaml` plus
   `fibermap.WithRoutesFS(routesFS)` → one binary, no
   working-directory traps in deployment.

4. **`Store` is an interface.** The in-memory impl is fine for a demo;
   swapping it for Postgres / SQLite / DynamoDB only requires
   implementing the same five methods. Handlers don't change.

5. **`fibermaptest` for "the YAML says what we think".**
   `main_test.go` asserts route counts, handler names, middleware
   chains, and tags directly against the loaded `routes.yaml` — so a
   merge that accidentally removes `require_role: [admin]` from DELETE
   fails CI immediately.

6. **`/admin/routes` for ops.** Tiny endpoint, big leverage — on-call
   can `curl ../admin/routes` and see the live route table without
   re-reading config.

7. **`bind.Body[T]` + `validator:` tags for request bodies.** Handlers
   declare the request struct with `validate:` tags
   (`required`, `min`, `max`, `omitempty`, ...) and the handler is
   one-liner `req, err := bind.Body[createReq](c.Ctx, h.Validator)`.
   Cross-field rules that don't fit tags ("at least one of title,
   done") stay as hand-rolled checks after `bind.Body` succeeds.

8. **Built-in response cache with per-user KeyBy.** Read-only routes
   (`GET /tasks`, `GET /tasks/:id`) declare `cache:` directly in YAML
   — scalar form (`cache: 10s`) for just a TTL or a mapping for
   `control`/`headers`/`vary_header`. `main.go` wires the engine-wide
   defaults via `eng.SetCacheDefaults(fibermap.CacheDefaults[AppCtx]{
   KeyBy: c.Data.UserID })` so the cache namespace is per-user —
   alice's list is never served to bob. Default storage is Fiber's
   in-process map (fine for a single instance); swap `Storage:
   redis.New(...)` from
   [`gofiber/storage`](https://github.com/gofiber/storage) for a
   shared cache across replicas.

9. **`eng.Run(...)` instead of hand-rolled lifecycle.** `main.go` uses
   the one-call launcher: `WithFiberConfig` plugs in the custom
   `ErrorHandler`, `WithUse(fibermap.RequestID(), auth.Bearer())`
   installs the two Fiber-level middlewares (the built-in `RequestID`
   replaces a hand-rolled 8-line copy), `WithRoutesFS(routesFS)`
   loads the embedded YAML. SIGINT/SIGTERM with a 10s drain is the
   default — no manual `signal.NotifyContext` / `ShutdownWithContext`
   boilerplate.

10. **Security middleware via `WithUse`, in a fixed order.** `helmet` →
    `cors` → `limiter` → `auth`. Order is in `main.go` with the
    rationale spelled out as a comment: helmet decorates every
    response, cors must come before auth so OPTIONS preflight gets
    through, limiter must come before auth so anonymous flood doesn't
    pay for credential lookup, auth last so locals are populated for
    `ContextBuilder`.

11. **Basic-auth passwords are bcrypt-hashed.** `internal/auth/auth.go`
    stores `{user → bcryptHash}` and verifies via
    `bcrypt.CompareHashAndPassword`. Every failure branch — unknown
    user, wrong password, malformed header — returns one identical 401
    body so the demo doesn't leak whether a username exists. Demo
    logins still work: `alice:secret`, `bob:secret`, `root:admin`.

## What you'd add for real production

Most of the boring stuff is already wired in this example (env config,
body limit, helmet, CORS, per-IP rate limit, typed error responses,
graceful shutdown, metrics). What's still demo-only:

- Replace `tasks.NewMemStore()` with a database-backed `Store`
  (Postgres / SQLite / DynamoDB — handlers won't change, only the
  store impl).
- Replace `demoTokens` / `demoBasic` with a real verifier — a JWT
  library hitting your IdP's JWKS, an opaque-token Redis lookup, or
  whatever your auth model is.
