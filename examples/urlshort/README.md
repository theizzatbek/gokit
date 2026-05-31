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
| `gokit/db` | Postgres pool + `Query/Exec`; unique-violation surfaces as `errs.AlreadyExists`. `links.ListByUser` uses `ReadQuery` so the listing rides a replica when `DB_HAS_READ_REPLICA=true` (lag-tolerant read). |
| `gokit/db/sqb` | Squirrel builders + `sqb.Query/QueryRow/Exec`; every SQL in `users/service.go` and `links/service.go` flows through it (no heredoc strings). |
| `gokit/auth` | JWT issue/verify, argon2id hashing, `auth.Auth.IssueLogin/IssueRefresh/Logout` (your handler parses the body and calls them) |
| `gokit/auth/refreshpg` | Refresh tokens persisted in Postgres (`auth_refresh_tokens` table) |
| `gokit/auth/fibermount` | Mounts `bearer`/`require_scope`/`require_role` factory middleware into the engine |
| `gokit/clients/httpc` | `enrich.Fetcher` does arbitrary-URL fetch to parse `<title>` from HTML |
| `gokit/clients/apimap` | Declarative `microlink` client; `base_url` from `${MICROLINK_BASE_URL}` env |
| `gokit/clients/nats` | JetStream publish of `urlshort.link.{created,visited}` on stream `URLSHORT` |
| `gokit/db/outbox` | v2 outbox: `LinkCreated` enqueued via `outbox.EnqueueTyped` INSIDE the Create transaction; `service.WithOutbox` auto-wires the worker; `pg_notify` wakes the dispatcher within ~ms of commit; 7-day retention sweeps published rows. |

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

### Redirect hot path

```
GET /:code
  │
  ▼
fibermap.Engine (rate_limit 50/100/IP via auth factory)
  │
  ▼
links.Service.Resolve
  ├─ Redis cache.Get(code)
  │     positive hit  → return CachedLink
  │     negative hit  → return 404 (no DB)
  │     miss          ↓
  ├─ Postgres SELECT … WHERE code = $1
  │     hit  → cache.Set(code), continue
  │     miss → cache.SetNotFound(code), return 404
  ├─ pub.LinkVisited (fire-and-forget JetStream publish)
  ▼
302 Location: original_url
```

Three layers absorb scanner / hot-code traffic before it reaches
Postgres:

1. **Rate limit** at the route — 50 rps sustained per source IP,
   burst 100. Returns 429 with `Retry-After`.
2. **Negative cache** — the first 404 for an unknown code stores a
   60s sentinel in Redis. Subsequent hits to that code return 404
   without a DB round-trip.
3. **Positive cache** — code → `{ID, UserID, OriginalURL}` cached
   for 1h. `visit_count` + `last_visited_at` deliberately NOT
   cached (they mutate every click; caching them would defeat the
   purpose).

Invalidation: `Update` / `Delete` drop the cache entry after the DB
write succeeds, so the next `Resolve` refetches.

`REDIS_URL` env enables the cache; leaving it empty falls back to a
direct-Postgres path so the example still runs in dev.

### Batched visit counting

`urlshort.link.visited` is consumed via natsmap's batched-handler
mode: `subscribers.yaml` declares
`batch_size: 1000` + `batch_interval: 1s`, and
`natsmap.RegisterBatchedHandler[events.LinkVisited]` binds the
`link_visit_counter` subscriber to `links.VisitCounter.Handle`.
Under the hood natsmap opens a JetStream Pull subscription, fetches
up to 1000 messages with a 1s deadline, and hands them to Handle as
one slice. The handler aggregates events by code (domain-side
decision: many visits on a popular code collapse into one row) and
runs ONE statement:

```sql
UPDATE links AS l
SET visit_count = l.visit_count + v.delta,
    last_visited_at = greatest(
        coalesce(l.last_visited_at, 'epoch'::timestamptz),
        v.ts)
FROM (VALUES
    ($1::text, $2::bigint, $3::timestamptz),
    ($4,        $5,         $6),
    …
) AS v(code, delta, ts)
WHERE l.code = v.code;
```

One DB round-trip per second, regardless of click rate. Hot codes
no longer serialise on a single row-level write lock — the redirect
returns in ~1ms even under load.

`subscribers.yaml` declares the binding by name only; natsmap
auto-derives `durable = "link_visit_counter"` and `queue_group =
"link_visit_counter"` (see `resolveDurableQueueGroup` in
`clients/natsmap/engine.go`), so horizontal scaling never
double-counts.

**Delivery semantics — at-least-once via JetStream Pull + atomic
ack.** natsmap's batched dispatcher runs in Pull mode; messages
are NOT auto-acked on receipt. The handler's return drives the
entire batch's ack/nak status:

- `Handle` returns nil → kit Acks every message in the slice
  (atomic with the DB UPDATE — both succeed together).
- `Handle` returns err → kit Naks every message; JetStream
  redelivers the whole batch on the next fetch.

A crash mid-Handle (after `db.Exec` but before the kit's ack walk)
results in redelivery — the DB UPDATE was committed but the ack
wasn't sent. The handler is idempotent enough for this not to
matter for visit counts (a re-applied UPDATE bumps the count
again — over-count, never under-count). Strict-once
deployments would need a separate dedup table keyed by NATS
sequence number; out of scope for this example.

Subscription lifecycle is owned by natsmap — `service.Close` calls
`Runtime.Drain` which stops the pull loop and unsubscribes
gracefully. No explicit `VisitCounter.Close`.

### Idempotent Create

Migration `0002_idempotent_links.sql` adds `UNIQUE (user_id,
original_url)`. `links.Service.Create` pre-checks via SELECT and
falls back to fetch-on-conflict if a concurrent request wins the
race — two posts of the same URL from one user return the same code
without duplicate rows.

### Security hardening

The deployed surface ships with kit-default OWASP-baseline protections; this example tightens a few extras on top:

- **`/readyz`** — auto-mounted by `service.New`; runs DB + NATS + Redis ping in parallel under a 5s deadline. K8s readiness probe target. Distinct from `/healthz` which is always 200.
- **Security headers** — `service.New` auto-installs `fibermap.SecurityHeaders`: HSTS (1y), `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: strict-origin-when-cross-origin`, an API-friendly CSP.
- **64 KiB body limit** — `service.WithBodyLimit(64*1024)` in `main.go`. Fiber returns 413 above the cap before the handler allocates a request buffer.
- **Per-route rate limits** declared in `configs/routes.yaml` via the auth `rate_limit` factory:
  - `POST /auth/register` — 1 rps / burst 5 per IP (mass-signup guard).
  - `POST /auth/login` — 2 rps / burst 10 per IP (credential stuffing).
  - `POST /auth/refresh` — 1 rps / burst 5 per IP (cookie probing).
  - `POST /links` — 5 rps / burst 20 per IP (authenticated abuse cap).
  - `GET /:code` — 50 rps / burst 100 per IP (scanner absorption; already documented above).

429s from the rate limiter expose the stable `rate_limited` Code so the client UI can show a "slow down" message instead of leaking a "wrong password" hint to an attacker.

### LinkCreated through the transactional outbox

The Create handler runs INSERT + `outbox.Enqueue` in ONE `db.Tx`, so the link row and the `LinkCreated` event commit atomically. A long-lived `outbox.Worker` started in `main.go` polls the table on a 5-second cadence, calls `natsmap.PublishRaw` per event, and marks the row published on success — bumps `attempts` and stashes the error otherwise.

Why bother for a click-tracking demo? Because the **commit→publish crash window** is exactly the kind of bug that escapes integration tests and surfaces only in production: the link is durable, the downstream "user got their new short URL" notification never fires, no error gets logged anywhere. The outbox pushes the publish step into a separate retryable transaction, so a crash anywhere in the pipeline either rolls back the entire link creation OR delivers the event eventually.

`LinkVisited` deliberately stays on the direct publish path — fire-and-forget analytics, bounded loss on a node crash is acceptable, and the outbox storage cost (one INSERT per click) would dominate the redirect hot path's latency budget.

## Limitations

- **Best-effort enrichment:** if MicroLink or the target URL is down, the link is still created with empty metadata. Not a bug — the demo deliberately picks "user-visible failures should be loud; analytics should be quiet".
- **6-char base62 code:** ~1e10 keyspace; retry up to 5 times on unique-violation, then error. Increase length for higher volume.
- **At-most-once visit counting** during the ≤1s buffer window (see above). Production deployments needing strict counts should switch to a manual-ack JetStream subscription.
- **No HTTPS, no real secrets handling** — dev only.
- **Refresh-token rotation works** but no per-device tracking beyond `user_agent`.

## Tests

```bash
make test    # requires Docker — testcontainers Postgres + NATS + httptest stub
```

One end-to-end smoke test (`main_test.go::TestSmoke_EndToEnd`) covers
every package in a single positive-path scenario. Negative cases live
in each subpackage's own test suite.
