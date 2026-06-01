# urlshort — multi-service gokit-пример

URL-shortener, разнесённый на **четыре сервиса** — демонстрирует
production-pattern с явным "edge-gateway"-разделением: api **не
импортирует** NATS вообще. Все события идут через
urlshort-publisher — единственный сервис, который знает про NATS
bus. Другие worker'ы consume'ят из NATS в обычном async-режиме.

```
       ┌─────────────────────┐                  ┌─────────────────────────┐
       │   urlshort-api      │                  │   urlshort-publisher    │
       │  HTTP + DB only     │                  │  HTTP→NATS gateway      │
       │  (no NATS imports)  │                  │  + outbox-worker        │
       │                     │                  │                         │
       │  • POST /links      │                  │  • POST /publish        │
       │  • GET /:code       │                  │    {subject, payload}   │
       │  • auth flows       │                  │  • drains outbox-table  │
       │  • owns migrations  │                  │  • publishes to NATS    │
       │  • writes outbox    │  ── HTTP POST ─▶ │                         │
       │    in db.Tx         │   /publish for   │                         │
       │                     │   LinkVisited    │                         │
       └──────┬──────────────┘                  └──────────┬──────────────┘
              │                                            │
              │       outbox-table                         │ NATS publish
              ▼       (via shared Postgres)                ▼
       ┌──────────────────────────────────────────────────────────────┐
       │                          NATS bus                            │
       │           urlshort.link.created │ urlshort.link.visited     │
       └────────────────┬──────────────────────────┬──────────────────┘
                        │                          │
                        ▼                          ▼
               ┌───────────────────┐      ┌──────────────────────┐
               │ urlshort-enricher │      │  urlshort-counter    │
               │ apimap (Microlink)│      │  batched UPDATE      │
               │ → UPDATE title/...│      │  visit_count + last  │
               └───────────────────┘      └──────────────────────┘
```

## Сервисы

| Service | Owns | Wire |
|---|---|---|
| **urlshort-api** | HTTP routes; auth (JWT + refresh); CRUD `links`; **schema/migrations**; writes outbox rows in `db.Tx`. POSTs LinkVisited JSON to publisher. **Никакого natsmap-import'а.** | port 3000; HTTP only |
| **urlshort-publisher** | NATS-publish surface: `POST /publish` HTTP-endpoint (HTTP→NATS adapter for LinkVisited) **+** outbox-worker (drains the shared outbox table, publishes LinkCreated to NATS). | port 3001; HTTP + NATS + Postgres |
| **urlshort-counter** | Колонки `visit_count` + `last_visited_at`. Batched NATS consumer. | NATS sub `urlshort.link.visited` (batched 1000/1s) |
| **urlshort-enricher** | Колонки `title` + `description` + `image_url`. Calls Microlink + open-fetch HTML. | NATS sub `urlshort.link.created` (one-by-one) |

Shared:
- **`shared/events`** — payload types (`LinkCreated`, `LinkVisited`) + subject constants. **Только** cross-service зависимость.
- **`shared/migrations`** — единая DDL-схема. Owned by api.

## Что демонстрирует именно этот раскрой

| Pattern | Зачем |
|---|---|
| **api без NATS-import'а** | api deploy'ится в подсеть без NATS-доступа (DMZ-pattern, FaaS / Cloud Run / Lambda). Publisher — единственный point of contact с NATS-кластером. |
| **HTTP-gateway для async-events** | Polyglot-friendly. Сервис на Python/Java/Ruby может `POST /publish` без NATS-client'а. |
| **Outbox через shared Postgres** | api пишет outbox-row в Tx — никаких "что если publisher down между commit и publish". Publisher читает outbox через тот же Postgres, drain'ит. At-least-once delivery. |
| **Разные guarantees per event** | LinkCreated → outbox (strong, transactional). LinkVisited → HTTP POST fire-and-forget (analytics, bounded loss OK). Один сервис, два пути. |
| **Independent scaling** | api scales по HTTP-трафику. Publisher scales по event-throughput'у — если outbox backlog растёт, `kubectl scale urlshort-publisher --replicas=4`. |

## Endpoint'ы

### urlshort-api (port 3000)

| Method + Path | Описание |
|---|---|
| `POST /auth/register` | email + password (argon2id) |
| `POST /auth/login` | issue access JWT + refresh-cookie |
| `POST /auth/refresh` | rotate refresh-token |
| `POST /auth/logout` | revoke refresh |
| `POST /links` | shorten (empty metadata at insert; enricher backfills) |
| `GET /{code}` | 302 redirect + POST LinkVisited to publisher |
| `GET /links` | list my links |
| `PATCH /links/{code}` | update title/description (owner-only) |
| `DELETE /links/{code}` | owner-only delete |
| `GET /healthz`, `/readyz`, `/metrics`, `/preflight` | ops endpoints |
| `GET /openapi.json`, `/docs` | generated OpenAPI + Scalar UI |

### urlshort-publisher (port 3001)

| Method + Path | Описание |
|---|---|
| `POST /publish` | `{subject, payload, headers?}` JSON → `natsmap.PublishRaw(subject, payload)`. Returns 202 on accept. |
| `GET /healthz`, `/readyz`, `/metrics`, `/preflight` | ops endpoints |

## Как запустить

```bash
# 1. JWT signing key (для api)
openssl genpkey -algorithm ED25519

# 2. Скопировать env-template и вставить PEM
cp .env.example .env

# 3. Поднять Postgres + NATS + Redis
make up

# 4. В четырёх разных терминалах:
set -a; source .env; set +a
make run-api          # терминал 1 — applies migrations + outbox-DDL
make run-publisher    # терминал 2 — drains outbox + HTTP gateway
make run-counter      # терминал 3 — drains link.visited
make run-enricher     # терминал 4 — drains link.created
```

## Пример взаимодействия

```bash
# Register + login
curl -X POST localhost:3000/auth/register \
  -H 'content-type: application/json' \
  -d '{"email":"a@b.com","password":"hunter2hunter2"}'
TOKEN=$(curl -s -X POST localhost:3000/auth/login \
  -H 'content-type: application/json' \
  -d '{"email":"a@b.com","password":"hunter2hunter2"}' | jq -r .access_token)

# Shorten (instant — metadata empty initially)
curl -X POST localhost:3000/links \
  -H "Authorization: Bearer $TOKEN" \
  -H 'content-type: application/json' \
  -d '{"url":"https://go.dev"}'
# Pipeline: api INSERT links + outbox.Enqueue (in db.Tx) →
#           publisher drains outbox → NATS publish → enricher fetches
#           metadata → UPDATE row.

# Redirect (api POSTs LinkVisited JSON to publisher gateway)
curl -I localhost:3000/Ab1cD
# Pipeline: api 302 → goroutine POSTs to publisher /publish →
#           publisher republishes onto NATS →
#           counter batches → UPDATE visit_count.

# Direct gateway call — useful for replay / debugging
curl -X POST localhost:3001/publish \
  -H 'content-type: application/json' \
  -d '{
    "subject": "urlshort.link.visited",
    "payload": {"code":"Ab1cD","visited_at":"2026-06-01T12:00:00Z","ip":"1.2.3.4"}
  }'
# 202 Accepted — bypasses api entirely, useful when replaying
# captured analytics into the NATS bus.
```

## Topology-свойства

| Property | Achieved via |
|---|---|
| **At-least-once delivery** для `link.created` | api INSERTs into outbox table inside `db.Tx`. Publisher drains. Crash anywhere → outbox-worker resumes. |
| **Network-zone isolation** | api binary has zero natsmap import. Can ship into a DMZ where only Postgres + publisher are reachable. |
| **Bounded loss** для `link.visited` | api fires HTTP POST to publisher; failures logged + dropped. Acceptable analytics-loss. |
| **Eventual consistency** для metadata | api inserts row с empty title/description. Enricher backfills async. |
| **Horizontal scaling** counter + enricher | natsmap auto-derives queue_group per subscriber → fan-out across replicas. |
| **Schema-ownership centralized** | api owns migrations. Publisher / counter / enricher trust schema is present. |

## Layout

```
examples/urlshort/
├── README.md (← you are here)
├── Makefile                 # up / down / run-{api,publisher,counter,enricher}
├── docker-compose.yaml      # postgres + nats + redis
├── shared/
│   ├── events/              # LinkCreated, LinkVisited + subject constants
│   └── migrations/          # 0001_init.sql, 0002_idempotent_links.sql + embed.go
├── urlshort-api/            # HTTP + DB — no NATS
│   ├── main.go
│   ├── configs/routes.yaml
│   └── internal/{appctx,config,publisher,users,links}/
├── urlshort-publisher/      # HTTP→NATS gateway + outbox-worker
│   ├── main.go
│   ├── configs/{routes,publishers}.yaml
│   └── internal/gateway/    # POST /publish handler
├── urlshort-counter/
│   ├── main.go
│   ├── configs/subscribers.yaml
│   └── internal/counter/
└── urlshort-enricher/
    ├── main.go
    ├── configs/{subscribers,clients}.yaml
    └── internal/{enrich,enricher}/
```

## Чему учит этот пример

| Pattern | Где смотреть |
|---|---|
| Transactional outbox (just-INSERT side) | `urlshort-api/internal/links/service.go` — `outbox.EnqueueTyped` внутри `db.Tx` |
| Transactional outbox (drain side) | `urlshort-publisher/main.go` — `service.WithOutbox(...)` |
| HTTP→NATS gateway | `urlshort-publisher/internal/gateway/handler.go` — `POST /publish` → `natsmap.PublishRaw` |
| HTTP client side of gateway | `urlshort-api/internal/publisher/publisher.go` — POST JSON via `clients/httpc` |
| Batched NATS-subscriber | `urlshort-counter/internal/counter/counter.go` — aggregate + `UPDATE … FROM unnest` |
| One-by-one NATS-subscriber | `urlshort-enricher/internal/enricher/enricher.go` — straightforward per-msg-handler |
| Declarative apimap | `urlshort-enricher/configs/clients.yaml` — Microlink + open-fetch |
| Shared schema, separate binaries | `shared/migrations/embed.go` + `WithMigrations(migrations.FS())` в api |
| Redis read-through cache | `urlshort-api/internal/links/service.go` — `cache.For[CachedLink]` + Resolve |
| Read-replica routing | `urlshort-api/internal/links/service.go` — `db.ReadQuery` для ListByUser |
| OpenAPI auto-generation | `urlshort-api/configs/routes.yaml` — `openapi:` block + `WithOpenAPI()` |
| Singleton cron | `urlshort-api/main.go` — `AddSingletonCron("daily-stats", ...)` |
| Preflight checks | все четыре сервиса — `WithPreflightEndpoint("")` для `kit doctor` |

## .env.example

```
# Postgres (shared by api / publisher / counter / enricher)
DB_HOST=localhost
DB_PORT=5432
DB_USER=urlshort
DB_PASSWORD=urlshort
DB_NAME=urlshort

# NATS (publisher / counter / enricher only — api does NOT need it)
NATS_URL=nats://localhost:4222

# Redis (api only)
REDIS_URL=redis://localhost:6379

# JWT signing material (api only)
JWT_PRIVATE_KEY_PEM="-----BEGIN PRIVATE KEY-----\n...\n-----END PRIVATE KEY-----"

# Microlink (enricher only)
MICROLINK_BASE_URL=https://api.microlink.io

# api → publisher URL
PUBLISHER_URL=http://localhost:3001

# Short-URL base (api only)
SHORT_URL_BASE=http://localhost:3000

# Per-service config paths (each Makefile target sets CONFIGS_DIR=configs)
ROUTES_PATH=configs/routes.yaml
NATSMAP_PUBLISHERS_PATH=configs/publishers.yaml
NATSMAP_SUBSCRIBERS_PATH=configs/subscribers.yaml
APIMAP_PATH=configs/clients.yaml
```
</content>
