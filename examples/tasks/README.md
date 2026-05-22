# tasks — realistic fibermap example

A small per-user tasks/CRUD API meant as a **starting template**, not a
teaching demo. Copy this directory, rename, adjust, ship.

## What's actually in here (and why)

| Path | Why |
| --- | --- |
| `main.go` | wire-up; graceful shutdown on SIGINT/SIGTERM; structured `slog` logger; embedded `routes.yaml` via `embed.FS` (one binary, no on-disk file dependency) |
| `routes.yaml` | declarative route tree, mounted via `Engine.LoadFS`; modeline at the top gives editor autocomplete via JSON Schema |
| `internal/appctx/` | `AppCtx` struct (user_id, role, request_id, scoped logger) + `Ctx` / `H` / `MW` aliases so handler signatures don't carry the generic parameter |
| `internal/auth/` | Fiber-level Bearer-token middleware (runs **before** `ContextBuilder`) + fibermap factory `require_role` |
| `internal/tasks/` | domain — `Task` model, `Store` interface (memory impl behind it; swap for postgres without touching handlers), handlers with manual body validation |
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
   `eng.LoadFS(routesFS, "routes.yaml")` → one binary, no
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

## What you'd add for real production

- Replace `demoTokens` with a real JWT verifier or session store.
- Swap `tasks.NewMemStore()` for a database-backed `Store`.
- Add request-body size limits and a real validator (e.g.
  `go-playground/validator` on tagged struct fields).
- Wire metrics (Prometheus middleware) at the Fiber level.
- Use `getkin/kin-openapi` over `Engine.Walk()` to publish a real
  OpenAPI doc instead of just `/admin/routes`.
