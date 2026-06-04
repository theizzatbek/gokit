# gokit

Композируемый Go service kit. Около тридцати независимо импортируемых пакетов,
покрывающих то, что каждый HTTP API сервис делает руками: роутинг, ошибки,
БД, аутентификация, исходящие HTTP, NATS-стриминг, observability, resilience,
audit, schedulers, file uploads, webhook'и.

Каждый пакет можно использовать отдельно. Вместе они уводят вас от
`main.go` к сервису production-shape — либо постепенно (`fibermap` сам по
себе), либо одним вызовом через `service.New`.

**Статус:** 0.x — API нестабильно. Breaking changes возможны между minor'ами.

## Что в коробке

### Базовые блоки

| Пакет | Что делает |
|---|---|
| [`fibermap/`](fibermap/README.md) | YAML-декларативный роутер для Fiber v2. Хендлеры и middleware по именам. Типизированный per-request контекст. OpenAPI генерация. |
| [`errs/`](errs/README.md) | Типизированные доменные ошибки (`Kind`, `Code`, `Details`, `Cause`) с маппингом в HTTP. Только stdlib. |
| [`errs/errsval/`](errs/errsval/README.md) | Конверсия `validator.ValidationErrors` → `*errs.Error{KindValidation}`. |
| [`reqctx/`](reqctx/) | Request-ID propagation primitive. |

### База данных

| Пакет | Что делает |
|---|---|
| [`db/`](db/README.md) | pgx pool wrapper. Транзакции с savepoint'ами. Healthcheck. Маппинг ошибок в `*errs.Error`. |
| [`db/sqb/`](db/sqb/README.md) | Опциональная squirrel-обёртка, преднастроенная на `$N` placeholders. |
| [`db/migrate/`](db/migrate/README.md) | Zero-dependency migration runner на `embed.FS`. |
| [`db/lock/`](db/lock/README.md) | `pg_advisory_lock` примитив для leader-election и mutual exclusion. |
| [`db/jobs/`](db/jobs/README.md) | Postgres-backed delayed job queue (gap между cron'ом и outbox'ом). |
| [`db/outbox/`](db/outbox/README.md) | Transactional outbox pattern (publish-side). |
| [`db/outbox/outboxnats/`](db/outbox/outboxnats/README.md) | Adapter — `outbox.PublishFn` → natsmap. |
| [`db/inbox/`](db/inbox/README.md) | Inbox table для effectively-once consumer'ов. |
| [`db/inbox/inboxnats/`](db/inbox/inboxnats/README.md) | Adapter — natsmap handler wrapper с дедупликацией. |

### Аутентификация

| Пакет | Что делает |
|---|---|
| [`auth/`](auth/README.md) | JWT issue/verify (EdDSA/ES256), Argon2id hashing, refresh-token rotation, Fiber middleware. |
| [`auth/refreshpg/`](auth/refreshpg/README.md) | Postgres-backed `RefreshStore`. |
| [`auth/refreshredis/`](auth/refreshredis/README.md) | Redis-backed `RefreshStore` (Lua-atomic). |
| [`auth/apikeypg/`](auth/apikeypg/README.md) | Postgres-backed API-key store. |
| [`auth/sessions/`](auth/sessions/README.md) | Server-side cookie sessions с revocation. |
| [`auth/fibermount/`](auth/fibermount/README.md) | Bridge между `auth` и `fibermap` middleware factories. |

### Исходящий HTTP

| Пакет | Что делает |
|---|---|
| [`clients/httpc/`](clients/httpc/README.md) | `*http.Client` с retry / per-attempt timeout / breaker / bulkhead / observability. |
| [`clients/apimap/`](clients/apimap/README.md) | YAML-декларативные upstream API'и. Вызов по имени через `Decode[T]`/`Exchange[Req,Resp]`. |

### NATS / JetStream

| Пакет | Что делает |
|---|---|
| [`clients/nats/`](clients/nats/README.md) | Типизированная обёртка над JetStream. `Publisher[T]` / `Subscribe[T]`. |
| [`clients/natsmap/`](clients/natsmap/README.md) | Декларативные подписчики/публишеры через YAML. |
| [`clients/natsmap/natsgw/`](clients/natsmap/natsgw/README.md) | HTTP-gateway для NATS publish (для сервисов в network zone без NATS reachability). |

### Redis-инфраструктура

| Пакет | Что делает |
|---|---|
| [`clients/redis/`](clients/redis/README.md) | `*redis.Client` bootstrap с initial-PING retry. |
| [`clients/cache/`](clients/cache/README.md) | Типизированный Redis read-through cache с positive/negative TTL. |
| [`clients/ratelimit/`](clients/ratelimit/README.md) | Sliding-window rate limiter на Lua-скрипте. |

### Прочие clients

| Пакет | Что делает |
|---|---|
| [`clients/s3/`](clients/s3/README.md) | `aws-sdk-go-v2/service/s3` wrapper. AWS / MinIO / R2 / Spaces / B2. |
| [`clients/email/`](clients/email/README.md) | Pluggable transactional email (SMTP / SES / Postmark). |
| [`clients/webhooks/`](clients/webhooks/README.md) | Outbound + inbound webhook'и (Subscription/Delivery/Fanout/Worker, HMAC signing). |

### Resilience

| Пакет | Что делает |
|---|---|
| [`breaker/`](breaker/README.md) | 3-state circuit breaker (closed/open/half_open). |
| [`bulkhead/`](bulkhead/README.md) | Concurrency cap с bounded queue + опциональный adaptive controller (AIMD). |
| [`batch/`](batch/README.md) | Batched-handler dispatcher для bulk sink-операций. |

### Observability

| Пакет | Что делает |
|---|---|
| [`otelkit/`](otelkit/README.md) | OpenTelemetry bootstrap (tracing + metrics + logs bridges). |
| [`sentrykit/`](sentrykit/README.md) | Sentry error-tracking + Fiber middleware + slog→breadcrumb bridge. |

### Scheduling

| Пакет | Что делает |
|---|---|
| [`cronmap/`](cronmap/README.md) | Declarative cron scheduler с YAML + handler-by-name. Per-run timeout, singleton (pg-advisory-lock), Sentry crons slug — всё YAML-аттрибутами. См. также [`db/jobs/`](db/jobs/README.md) (Postgres-backed delayed/one-shot queue). |

### Operations

| Пакет | Что делает |
|---|---|
| [`audit/`](audit/README.md) | Append-only audit-log infrastructure (SOC2 / HIPAA / PCI-DSS). |
| [`runbook/`](runbook/README.md) | Runtime kill-switch для ops без redeploy. |
| [`fibermap/uploadguard/`](fibermap/uploadguard/README.md) | File-upload validation middleware (pairs с `clients/s3`). |

### Bundle

| Пакет | Что делает |
|---|---|
| [`service/`](service/README.md) | `service.New(ctx, cfg, opts...)` всё-в-одном wiring. Auto-detect optionality + startup log + Status() introspection. |

## Decision guide — "мне нужно X"

| Задача | Бери |
|---|---|
| Описать routes снаружи кода | `fibermap` + `routes.yaml` |
| Generic типизированные доменные ошибки | `errs` (+ `errsval` если есть validator) |
| Postgres connection pool + транзакции | `db` |
| SQL builder | `db/sqb` |
| Прокатить миграции из embed.FS | `db/migrate` |
| Leader-election в multi-replica | `db/lock` |
| Один pod слушает, остальные нет | `db/lock` |
| Periodic cron jobs (declarative YAML) | `cronmap` (+ `service.WithCronMap` если используешь bundle) |
| Cron job с leader-elect (один pod из N) | `cronmap` + `singleton: true` (нужен DB) |
| One-shot delayed job ("через 5 мин сделай X") | `db/jobs` |
| Гарантированный publish после db.Commit | `db/outbox` + `db/outbox/outboxnats` |
| Effectively-once consumer (не делать дважды) | `db/inbox` + `db/inbox/inboxnats` |
| JWT-based authn | `auth` + `auth/refreshpg`/`refreshredis` |
| Cookie-based sessions с revocation | `auth/sessions` |
| API key authn | `auth/apikeypg` |
| HTTP-клиент с retry'ями + observability | `clients/httpc` |
| Описать upstream API'и в YAML | `clients/apimap` |
| Защититься от падающего апстрима | `breaker` (или apimap `breaker:` блок) |
| Защититься от заевшего апстрима | `bulkhead` (или apimap `bulkhead:` блок) |
| Publish событий в NATS JetStream | `clients/nats` или `clients/natsmap` |
| YAML-декларативные NATS-подписчики | `clients/natsmap` |
| Bulk-aggregate event stream → 1 DB write | `batch` |
| Read-through cache на Redis | `clients/cache` |
| Sliding-window rate-limit (shared across replicas) | `clients/ratelimit` |
| Upload файлов с validation | `fibermap/uploadguard` + `clients/s3` |
| Послать email | `clients/email` |
| Outbound webhook'и с retry + DLQ | `clients/webhooks` |
| Append-only audit log | `audit` |
| Runtime kill-switch без redeploy | `runbook` |
| Tracing + metrics + log bridges | `otelkit` |
| Sentry error tracking | `sentrykit` |
| Всё сразу одним вызовом | `service` |

## Правила зависимостей

```
errs                                        → только stdlib
reqctx                                      → только stdlib
db, db/sqb, db/migrate, db/lock             → errs + pgx
db/jobs, db/outbox, db/inbox                → db + errs
db/outbox/outboxnats, db/inbox/inboxnats    → db/outbox|inbox + clients/natsmap
breaker, bulkhead, batch                    → stdlib + prometheus
cronmap                                     → errs + robfig/cron + yaml.v3 (+ db/lock optional for PGLocker, sentrykit optional)
clients/httpc                               → errs + prometheus + breaker + bulkhead
clients/apimap                              → errs + clients/httpc + breaker + bulkhead + yaml.v3
clients/nats                                → errs + nats.go + prometheus
clients/natsmap                             → errs + clients/nats + yaml.v3
clients/redis, clients/cache, clients/ratelimit → errs + go-redis
clients/s3                                  → errs + aws-sdk-go-v2
clients/email                               → errs + (provider-specific)
clients/webhooks                            → errs + db + clients/httpc
auth                                        → errs + crypto + jwt + fiber
auth/refreshpg, auth/apikeypg, auth/sessions → auth + db
auth/refreshredis                           → auth + go-redis
auth/fibermount                             → auth + fibermap
fibermap                                    → errs + fiber
fibermap/uploadguard                        → fibermap + clients/s3
otelkit, sentrykit, runbook                 → fibermap + provider SDK
audit                                       → errs + db (audit/auditpg)
service                                     → почти всё (это и есть all-in-one)
```

Корневой пакет `gokit` пустой — без экспортируемых символов. Импорт одного
подпакета не тянет остальные (`service` — единственное исключение).

## Установка

```bash
# Минимум для роутера:
go get github.com/theizzatbek/gokit/fibermap
go get github.com/theizzatbek/gokit/errs

# Всё-в-одном:
go get github.com/theizzatbek/gokit/service

# Опциональный CLI для linting'а routes.yaml + экспорта schema:
go install github.com/theizzatbek/gokit/cmd/fibermap@latest

# CLI для scaffold'а нового сервиса:
go install github.com/theizzatbek/gokit/cmd/kit@latest
```

Требуется Go 1.23+ и Fiber v2 (для `fibermap/`).

## Quickstart — fibermap роутер

```yaml
# routes.yaml
groups:
  - prefix: /v1
    routes:
      - method: GET
        path:   /ping
        handler: ping
        name:   ping.get
```

```go
package main

import (
    "context"

    "github.com/gofiber/fiber/v2"
    "github.com/theizzatbek/gokit/fibermap"
)

type AppCtx struct{ /* per-request data */ }

func main() {
    eng := fibermap.New[AppCtx]()
    eng.SetContextBuilder(func(c *fiber.Ctx) (AppCtx, error) {
        return AppCtx{}, nil
    })
    fibermap.RegisterHandler(eng, "ping", func(c *fibermap.Context[AppCtx]) error {
        return c.SendString("pong")
    })
    if err := eng.LoadFile("routes.yaml"); err != nil {
        panic(err)
    }
    if err := eng.Run(context.Background(), fibermap.WithAddr(":3000")); err != nil {
        panic(err)
    }
}
```

## Quickstart — service.New (полный bundle)

```go
svc, err := service.New[AppCtx, MyClaims](ctx, cfg,
    service.WithRoutes(),
    service.WithOpenAPI(),
    service.WithSentry(dsn, service.SentryOptions{
        ErrorCaptureLevel: service.LevelPtr(slog.LevelError),
    }),
)
if err != nil { return err }
defer svc.Close()

svc.SetContextBuilder(...)
fibermap.RegisterHandler(svc.Engine, ...)
return svc.Run()
```

На старте печатает один структурированный `service ready` log с booleanами
по каждому подсистеме (`db=true, auth=true, redis=false, ...`); intospection
через `svc.Status()`. Подробности — [`service/README.md`](service/README.md).

## Поддержка редактора для YAML-конфигов

Все встроенные YAML'ы кита (`routes.yaml`, `crons.yaml`, `clients.yaml`,
`subscribers.yaml`/`publishers.yaml`) имеют JSON Schema (draft-07),
сгружённые в [`schemas/`](schemas/). Добавьте modeline в начало
соответствующего файла:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/theizzatbek/gokit/main/schemas/routes.schema.json
```

VS Code (с [redhat.vscode-yaml]), GoLand и Vim с `coc-yaml` дают
автодополнение, hover-документацию и inline-диагностику — опечатки в
`middleware:` подсвечиваются до `go test`. Полный список схем + примеры
— в [`schemas/README.md`](schemas/README.md).

[redhat.vscode-yaml]: https://marketplace.visualstudio.com/items?itemName=redhat.vscode-yaml

## CLI

```bash
fibermap validate routes.yaml    # schema-lint; ненулевой exit при проблемах
fibermap dump-schema             # печатает встроенную JSON Schema
```

`validate` проверяет схему (обязательные поля, валидные HTTP методы, циклы в
middleware_set, форма middleware). НЕ проверяет, что имена handler /
middleware / factory зарегистрированы — ваш Go-бинарь это единственное место,
где они живут. Для полной валидации вызовите `Engine.Validate()` в Go-тесте
или boot-скрипте.

`cmd/kit` — отдельный generator/scaffold CLI ([см. `cmd/kit/README.md`](cmd/kit/README.md)).

## Примеры

| Пример | Что показывает |
|---|---|
| [`examples/urlshort/`](examples/urlshort/) | Multi-binary microservice setup с outbox + apimap. |
| [`examples/resilience/`](examples/resilience/) | breaker + bulkhead через apimap YAML против flaky httptest сервера. |
| [`examples/inbox-outbox/`](examples/inbox-outbox/) | Effectively-once event flow с outboxnats + inboxnats (testcontainers postgres + nats). |
