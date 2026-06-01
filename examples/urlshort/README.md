# urlshort — multi-service gokit-пример

URL-shortener, разнесённый на **три сервиса** — демонстрирует
типичный production-pattern: HTTP-frontend плюс async-worker'ы,
общающиеся через NATS + transactional outbox, с shared'ем
Postgres-схемы и общим NATS-кластером.

```
                        ┌──────────────────┐
                        │   urlshort-api   │
                        │  (HTTP + auth +  │
        ┌──── REST ─────│   outbox publish │─────── direct publish ────┐
        │               │   schema owner)  │                            │
        ▼               └──────────────────┘                            ▼
   browser / curl              │   ▲                            NATS subject
                               │   │                       urlshort.link.visited
              link.created     │   │ DB                            │
              (outbox path)    ▼   │                                │
                          ┌────────────────────┐         ┌──────────▼────────────┐
                          │ urlshort-enricher  │         │   urlshort-counter    │
                          │ NATS sub +         │         │   batched NATS sub +  │
                          │ apimap (Microlink) │         │   aggregated UPDATE   │
                          │ → UPDATE title/... │         │   visit_count + last  │
                          └────────────────────┘         └───────────────────────┘
                                    │                              │
                                    └────────── Postgres ──────────┘
```

## Сервисы

| Service | Owns | Listens / Publishes |
|---|---|---|
| **urlshort-api** | HTTP routes; auth (JWT + refresh); CRUD на `links`; миграции схемы; outbox-publish'ит `link.created`; direct-publish'ит `link.visited`. | publish `urlshort.link.created` + `urlshort.link.visited` |
| **urlshort-counter** | Колонки `visit_count` + `last_visited_at` на `links`. Read-write per-batch. | subscribe `urlshort.link.visited` (batched, 1000/1s) |
| **urlshort-enricher** | Колонки `title` + `description` + `image_url` на `links`. Calls Microlink + open-fetch HTML-title. | subscribe `urlshort.link.created` (one-by-one) |

Shared:
- **`shared/events`** — типы payload'ов (`LinkCreated`, `LinkVisited`) и subject-constants. Это единственная cross-service-зависимость.
- **`shared/migrations`** — единая DDL-схема. Owned by api (запускает на boot'е).

## Endpoint'ы (только api)

| Method + Path | Описание |
|---|---|
| `POST /auth/register` | email + password (argon2id) |
| `POST /auth/login` | issue access JWT + refresh-cookie (refresh in Postgres) |
| `POST /auth/refresh` | rotate refresh-token |
| `POST /auth/logout` | revoke refresh |
| `POST /links` | shorten (empty metadata at insert; enricher backfills) |
| `GET /{code}` | 302 redirect + publish `link.visited` |
| `GET /links` | list my links |
| `PATCH /links/{code}` | update title/description (owner-only) |
| `DELETE /links/{code}` | owner-only delete |
| `GET /healthz`, `/readyz`, `/metrics`, `/preflight` | ops-endpoints (auto-mounted) |
| `GET /openapi.json`, `/docs` | generated OpenAPI + Scalar UI |

## Как запустить

```bash
# 1. JWT signing key
openssl genpkey -algorithm ED25519

# 2. Скопировать env-template и вставить PEM
cp .env.example .env

# 3. Поднять Postgres + NATS + Redis
make up

# 4. В трёх разных терминалах:
set -a; source .env; set +a
make run-api        # терминал 1 — applies migrations, mounts HTTP
make run-counter    # терминал 2 — drains link.visited
make run-enricher   # терминал 3 — drains link.created, calls Microlink
```

В prod-деплое каждый сервис — отдельный pod (или ReplicaSet) с
своим Deployment + Service + ConfigMap. Используйте `kit gen k8s`
для генерации манифестов из `service.Config`-struct'а.

## Пример взаимодействия

```bash
# Register + login
curl -X POST localhost:3000/auth/register \
  -H 'content-type: application/json' \
  -d '{"email":"a@b.com","password":"hunter2hunter2"}'
TOKEN=$(curl -s -X POST localhost:3000/auth/login \
  -H 'content-type: application/json' \
  -d '{"email":"a@b.com","password":"hunter2hunter2"}' | jq -r .access_token)

# Shorten (instant; metadata is empty initially)
curl -X POST localhost:3000/links \
  -H "Authorization: Bearer $TOKEN" \
  -H 'content-type: application/json' \
  -d '{"url":"https://go.dev"}'
# {"code":"Ab1cD","title":"","description":"","image_url":"",...}

# … wait ~1s for enricher to consume link.created and call Microlink …
curl -s localhost:3000/links -H "Authorization: Bearer $TOKEN" | jq '.[0]'
# {"code":"Ab1cD","title":"The Go Programming Language", ...}

# Redirect (publishes link.visited)
curl -I localhost:3000/Ab1cD

# … wait ~1s for counter to batch the visit-event …
curl -s localhost:3000/links/Ab1cD/stats -H "Authorization: Bearer $TOKEN"
# {"visit_count":1, ...}
```

## Topology-properties

| Property | Achieved via |
|---|---|
| **At-least-once delivery** для `link.created` | transactional outbox: `INSERT links` + `outbox.Enqueue` в одной `db.Tx`. Crash между commit + publish → outbox-worker reschedules. |
| **Bounded loss** для `link.visited` | direct-publish, no outbox: JetStream-persisted, но crash before publish = потерянный click. Acceptable для analytics. |
| **Eventual consistency** для metadata | api insert'ит с пустыми `title`/`description`. Enricher UPDATE'ит асинхронно. Stale-state (enricher down) ≠ broken — redirect всё равно работает. |
| **Horizontal scaling** counter + enricher | natsmap auto-derives queue_group per subscriber name → fan-out across replicas, no double-fetch. |
| **Schema-ownership** centralized | api owns migrations; counter + enricher trust schema is present. Use `kit doctor` to verify before scaling out workers. |

## Layout

```
examples/urlshort/
├── README.md (← you are here)
├── Makefile                 # up / down / run-{api,counter,enricher} / test
├── docker-compose.yaml      # postgres + nats + redis
├── shared/
│   ├── events/              # LinkCreated, LinkVisited + subject-constants
│   └── migrations/          # 0001_init.sql, 0002_idempotent_links.sql + embed.go
├── urlshort-api/
│   ├── main.go              # service.New: routes + outbox + publishers
│   ├── configs/
│   │   ├── routes.yaml
│   │   └── publishers.yaml
│   └── internal/
│       ├── appctx/          # request-scoped context
│       ├── config/          # env → Config
│       ├── publisher/       # thin LinkVisited NATS publisher
│       ├── users/           # auth: register / login / refresh / logout
│       └── links/           # CRUD + redirect + cache + outbox-enqueue
├── urlshort-counter/
│   ├── main.go              # service.New: NATSMap subscriber
│   ├── configs/subscribers.yaml
│   └── internal/counter/    # batched aggregator + UPDATE
└── urlshort-enricher/
    ├── main.go              # service.New: NATSMap subscriber + apimap
    ├── configs/
    │   ├── subscribers.yaml
    │   └── clients.yaml     # Microlink + open-fetch endpoints
    └── internal/
        ├── enrich/          # apimap-side fetcher
        └── enricher/        # NATS-handler + UPDATE
```

## Чему учит этот пример

| Pattern | Где смотреть |
|---|---|
| Transactional outbox | `urlshort-api/internal/links/service.go` — `outbox.EnqueueTyped` внутри `db.Tx` |
| Direct NATS-publish | `urlshort-api/internal/publisher/publisher.go` — fire-and-forget |
| Batched NATS-subscriber | `urlshort-counter/internal/counter/counter.go` — aggregate + UPDATE … FROM unnest |
| One-by-one subscriber | `urlshort-enricher/internal/enricher/enricher.go` — straightforward per-msg-handler |
| Declarative apimap | `urlshort-enricher/configs/clients.yaml` — Microlink + open-fetch |
| Shared schema, separate services | `shared/migrations/embed.go` + `WithMigrations(migrations.FS())` в api |
| Redis read-through cache | `urlshort-api/internal/links/service.go` — `cache.For[CachedLink]` + Resolve |
| Read-replica routing | `urlshort-api/internal/links/service.go` — `db.ReadQuery` для ListByUser |
| OpenAPI auto-generation | `urlshort-api/configs/routes.yaml` — `openapi:` block + `WithOpenAPI()` |
| Singleton cron | `urlshort-api/main.go` — `AddSingletonCron("daily-stats", ...)` |
| Preflight checks | все три сервиса — `WithPreflightEndpoint("")` для `kit doctor` |
| Multi-service-coordination | `docker-compose.yaml` + `make run-{api,counter,enricher}` |

## .env.example

```
# Postgres
DB_HOST=localhost
DB_PORT=5432
DB_USER=urlshort
DB_PASSWORD=urlshort
DB_NAME=urlshort

# NATS
NATS_URL=nats://localhost:4222

# Redis (optional — без него api работает без cache'а)
REDIS_URL=redis://localhost:6379

# JWT signing material (api only)
JWT_PRIVATE_KEY_PEM="-----BEGIN PRIVATE KEY-----\n...\n-----END PRIVATE KEY-----"

# Microlink (enricher only)
MICROLINK_BASE_URL=https://api.microlink.io

# Short-URL base (api only — для JSON response'ов)
SHORT_URL_BASE=http://localhost:3000

# Per-service routes/subscribers paths
ROUTES_PATH=configs/routes.yaml
NATSMAP_PUBLISHERS_PATH=configs/publishers.yaml
NATSMAP_SUBSCRIBERS_PATH=configs/subscribers.yaml
APIMAP_PATH=configs/clients.yaml
```
</content>
