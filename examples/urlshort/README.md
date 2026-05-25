# urlshort — gokit integration example

A URL-shortener that uses every gokit package in its natural role. Copy
`examples/urlshort/` as a template when starting a new service. All
wiring is visible in `main.go` — no hidden DI container.

## What it does

- `POST /auth/register` — create a user (email + password, argon2id)
- `POST /auth/login` — issue access JWT + refresh-cookie (refresh persisted in Postgres)
- `POST /auth/refresh` — rotate the refresh token, get a fresh access JWT
- `POST /auth/logout` — revoke the refresh token
- `POST /links` — shorten a URL. Fetches `<title>` via `httpc`, plus description + image via `apimap` calling MicroLink. Publishes `urlshort.link.created` to NATS.
- `GET /{code}` — 302-redirect, increment visit count, publish `urlshort.link.visited`
- `GET /links` — list my links (auth)
- `GET /links/{code}/stats` — owner-only visit stats (auth)
- `DELETE /links/{code}` — owner-only delete (auth)
- `GET /healthz`, `GET /metrics` — ops endpoints (auto-wired by `fibermap.Run`)
- `GET /openapi.json`, `GET /docs` — generated OpenAPI spec + Scalar UI

## How to run

```bash
# 1. Generate a JWT signing key (PEM Ed25519)
openssl genpkey -algorithm ED25519

# 2. Copy .env.example to .env and paste the PEM into JWT_PRIVATE_KEY_PEM
cp .env.example .env

# 3. Start local infrastructure (Postgres + NATS)
make up

# 4. Run the service
set -a; source .env; set +a
make run
```

### Sample interaction

```bash
# Register
curl -X POST http://localhost:3000/auth/register \
  -H 'content-type: application/json' \
  -d '{"email":"a@b.com","password":"hunter2hunter2"}'

# Login → capture access token
TOKEN=$(curl -s -X POST http://localhost:3000/auth/login \
  -H 'content-type: application/json' \
  -d '{"login":"a@b.com","password":"hunter2hunter2"}' | jq -r .access_token)

# Shorten
curl -X POST http://localhost:3000/links \
  -H "authorization: Bearer $TOKEN" \
  -H 'content-type: application/json' \
  -d '{"url":"https://go.dev"}'

# Follow the redirect
curl -I http://localhost:3000/<code>

# Stats
curl -H "authorization: Bearer $TOKEN" http://localhost:3000/links/<code>/stats
```

## Which gokit package does what here

| Package | Role |
|---|---|
| `gokit/fibermap` | HTTP routes declared in `routes.yaml`; `ContextBuilder` injects `AppCtx{UserID, Log}` |
| `gokit/fibermap/openapi` | `GET /openapi.json` + `GET /docs` served from `Generator.Mount()` |
| `gokit/fibermap/bind` | Request body decoding + validation for register/shorten |
| `gokit/errs` | All service errors are `*errs.Error`; `fibermap.ErrorHandler` maps to wire shape |
| `gokit/db` | Postgres pool + `Query/Exec`; unique-violation surfaces as `errs.AlreadyExists` |
| `gokit/auth` | JWT issue/verify, argon2id hashing, `auth.Auth.LoginHandler/RefreshHandler/LogoutHandler` |
| `gokit/auth/refreshpg` | Refresh tokens persisted in Postgres (`auth_refresh_tokens` table) |
| `gokit/auth/fibermount` | Mounts `bearer`/`require_scope`/`require_role` factory middleware into the engine |
| `gokit/clients/httpc` | `enrich.Fetcher` does arbitrary-URL fetch to parse `<title>` from HTML |
| `gokit/clients/apimap` | Declarative `microlink` client; `base_url` from `${MICROLINK_BASE_URL}` env |
| `gokit/clients/nats` | JetStream publish of `urlshort.link.{created,visited}` on stream `URLSHORT` |

## Architecture

```
                       ┌────────────────────────────────┐
                       │       client (curl / HTTP)      │
                       └──────────────┬─────────────────┘
                                      │
                                      ▼
                          ┌────────────────────────┐
                          │  fiber.App              │
                          │  + Bearer(Optional)     │ ← populates Locals
                          │  + fibermap.Engine[T]   │
                          │  + bearer factory mw    │ ← enforces per-route
                          └───────┬────────────────┘
                                  │
              ┌───────────────────┼──────────────────────────┐
              ▼                   ▼                          ▼
      users.Service        links.Service              auth.Auth[Claims]
       (db)                  (db, enrich,              (refreshpg, hasher)
                              events.PublishCreated,
                              events.PublishVisited)
                                  │
              ┌───────────────────┼────────────────────────┐
              ▼                   ▼                        ▼
      enrich.Fetcher       events.Publishers      gokit/db pool
       (httpc + apimap)     (natsclient)           (pgx)
              │                   │
              ▼                   ▼
      external HTML       NATS JetStream
      + MicroLink          (URLSHORT stream)
```

The Bearer-optional layer at `fiber.App.Use` populates `Locals` before
the engine's `ContextBuilder` runs — without it `AppCtx.UserID` would
be empty in handlers (because per-route `bearer: []` middleware runs
AFTER `contextInit`). Per-route `bearer: []` still enforces 401 on
protected paths.

## Limitations

- **Best-effort enrichment:** if MicroLink or the target URL is down, the link is still created with empty metadata. Not a bug — the demo deliberately picks "user-visible failures should be loud; analytics should be quiet".
- **6-char base62 code:** ~1e10 keyspace; retry up to 5 times on unique-violation, then error. Increase length for higher volume.
- **No rate-limit** (fibermap ships no rate-limit middleware in v0.x).
- **No HTTPS, no real secrets handling** — dev only.
- **Refresh-token rotation works** but no per-device tracking beyond `user_agent`.

## Tests

```bash
make test    # requires Docker — testcontainers Postgres + NATS + httptest stub
```

One end-to-end smoke test (`main_test.go::TestSmoke_EndToEnd`) covers
every package in a single positive-path scenario. Negative cases live
in each subpackage's own test suite.
