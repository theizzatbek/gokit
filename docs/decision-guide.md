# Decision guide — "мне нужно X"

Быстрая таблица: «у меня есть задача — какой пакет взять». Краткое
описание каждого пакета — в [README](../README.md#что-в-коробке);
полные API-контракты — в каждом package'овом `README.md` / `doc.go`.

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
