# db/outbox

Паттерн transactional-outbox для Postgres-backed kit-сервисов. События
пишутся в таблицу `outbox` внутри той же транзакции, что и бизнес-состояние;
фоновый `Worker` диспатчит их на реальную шину (NATS / Kafka)
с at-least-once доставкой.

## Зачем это нужно

Без outbox'а сервисы, которые делают "commit then publish", имеют
крэш-окно между двумя. DB-строка durable; событие — нет. Перезапуск
между commit и publish означает, что downstream-consumer'ы пропустят
событие навсегда. outbox закрывает это окно, делая publish-шаг
частью отдельной retryable-транзакции.

Используйте этот пакет, когда нужно одно из:

- Связать DB state changes с downstream pub/sub событиями, не теряя
  ни одно из них от крэша.
- At-least-once доставка поверх любой pub/sub-системы (пакет
  publish-agnostic — caller предоставляет `PublishFn`).
- Multi-replica safe drainer (`SELECT ... FOR UPDATE SKIP LOCKED`
  встроен в worker-запрос).

## Quickstart

Кратчайший путь — пусть `service.New` подключит всё:

```go
svc, _ := service.New[Ctx, Claims](ctx, cfg,
    service.WithNATSMap(),
    service.WithOutbox(
        outbox.WithRetention(7*24*time.Hour),
    ),
    service.WithOutboxAutoSchema(), // идемпотентно применяет schema.sql
)
```

В domain-сервисе enqueue'те внутри окружающей транзакции через
typed-обёртку:

```go
err := svc.DB.Tx(ctx, func(tx *db.Tx) error {
    if _, err := svc.LinksRepo.Create(ctx, tx, link); err != nil {
        return err
    }
    return outbox.EnqueueTyped(ctx, tx, "urlshort.link.created",
        events.LinkCreated{LinkID: link.ID, Code: link.Code, ...},
        outbox.WithAggregate("link", link.Code))
})
```

Для не-`service.New` caller'ов используйте building block'и напрямую:

```go
_, _ = svc.DB.Exec(ctx, outbox.Schema())            // применить DDL
w, _ := outbox.NewWorker(svc.DB,
    func(ctx context.Context, e outbox.Event) error {
        return natsmap.PublishRaw(ctx, svc.NATSMap,
            e.EventType, e.Payload, e.Headers)
    },
    outbox.WithLogger(svc.Logger()),
    outbox.WithMetrics(reg),
    outbox.WithRetention(7*24*time.Hour),
)
_ = w.Start(ctx)
svc.OnShutdown(w.Stop)
```

## Readiness-проверка

`outbox.NewChecker(d, opts...)` — это реализация [`fibermap.Checker`](../../fibermap/README.md), которая поднимает outbox-backlog на `/readyz`. `service.WithOutbox` авто-добавляет её; тюньте через `service.WithOutboxReadinessOpts(...)` или отключите через `service.WithoutOutboxReadiness()`.

| Опция | По умолчанию | Заметки |
|---|---|---|
| `WithMaxDepth(n)` | 10000 | Pending-row count выше этого → 503 + `outbox_backlog` code. |
| `WithMaxLag(d)` | 10m | Возраст самой старой pending-строки выше этого → 503 + `outbox_backlog` code. |
| `WithCheckerName(name)` | "outbox" | Имя, появляющееся под `checks: {…}` в 503 body. |

Проверка выполняет `SELECT count(*), MIN(created_at)` по partial-индексу, который worker и так использует — не нужен extra-индекс.

## Trace context

`Enqueue` снапшотит текущий OTel `TraceContext` (W3C `traceparent` / `tracestate`) в `Event.Headers`, чтобы более поздний publish Worker'а сохранял originating-трассу через async-границу. inject-путь kit'ового `natsclient.PublishRaw` рассматривает pre-existing `traceparent` в headers как authoritative — обычно-trace-less ctx worker'а никогда не перезаписывает snapshot. Результат: HTTP → Tx → outbox-строка → Worker → consumer все шарят один trace ID в APM-водопаде.

Setup не нужен — propagation активируется автоматически, как только `TextMapPropagator` установлен глобально (`otel.SetTextMapPropagator(propagation.TraceContext{})`, что `service.WithOtel` делает).

## Схема (v2)

`schema.sql` **идемпотентен** и для fresh-инсталляций, и для v1 → v2
апгрейдов. Колонки:

| Колонка | Заметки |
|---|---|
| `id uuid PRIMARY KEY` | DB-generated UUID — появляется в `Event.ID`. |
| `aggregate_type, aggregate_id text` | Опциональные aggregate-shape labels. |
| `event_type text NOT NULL` | Bus subject, на который Worker диспатчит. |
| `payload bytea NOT NULL` | Opaque wire-bytes — JSON, protobuf, что угодно. |
| `headers jsonb` | Per-event metadata (W3C traceparent, и т.д.). |
| `created_at, published_at timestamptz` | Lifecycle штампы. |
| `attempts integer, last_error text` | Retry-bookkeeping. |
| `next_retry_at timestamptz NOT NULL DEFAULT NOW()` | **v2** — per-row backoff "ready at". |

Индексы:

- `outbox_pending_idx (next_retry_at, created_at) WHERE published_at IS NULL` — polling SELECT касается только строк, чьё retry-окно наступило.
- `outbox_aggregate_idx (aggregate_type, aggregate_id)` — для replay-тулзов.

Используйте `outbox.Schema()` один раз на boot (или впихните в свой migration-тул); `service.WithOutboxAutoSchema()` делает это автоматически.

## Семантика Worker'а

- **Polling**: `SELECT ... WHERE published_at IS NULL AND next_retry_at <= NOW() ORDER BY next_retry_at, created_at LIMIT $batch_size FOR UPDATE SKIP LOCKED`. Multi-replica safe — два worker'а, дренящих одну таблицу, не сталкиваются.
- **LISTEN/NOTIFY fast path** (v2, default-on): выделенное pool-соединение LISTEN'ит на `outbox_new`; Enqueue запускает `pg_notify('outbox_new', '')` после INSERT, так что commit-to-publish latency ~ms, а не ждёт следующего polling-tick'а. Polling остаётся как fallback для crash-recovery / dropped NOTIFY. Отключите через `WithoutListen()` при работе за connection pooler'ом, который ломает NOTIFY (PgBouncer transaction mode).
- **Per-row exponential backoff** (v2): на failure'е `next_retry_at = NOW() + base * 2^(attempts-1)` capped at max. Defaults: base 1s, max 1h. Останавливает failed-события от молотьбы по шине каждый polling-tick.
- **Per-event dispatch**: `PublishFn(ctx, Event) error`. Возврат nil
  маркирует строку published; возврат ошибки бампит `attempts` и
  записывает сообщение в `last_error`.
- **Retries**: unbounded по умолчанию — failed-события остаются в
  unpublished-set'е, и worker ретраит их на следующем tick'е.
  Cap'ните через `WithMaxAttempts(n)`.
- **Dead-lettering**: строки с `attempts >= max_attempts` остаются в
  таблице, но фильтруются из SELECT. Операторы решают disposition
  (delete, replay, archive).
- **At-least-once контракт**: крэш ПОСЛЕ успеха `PublishFn`, но ДО
  UPDATE строки приведёт к redelivery. Downstream-consumer'ы должны
  dedupe — установите шинный `Nats-Msg-Id` (или эквивалент) в
  `Event.ID`.

## Опции

| Опция | По умолчанию | Заметки |
|---|---|---|
| `WithInterval(d)` | 5s | Polling-cadence (LISTEN/NOTIFY обычно будит worker раньше; это fallback). |
| `WithBatchSize(n)` | 100 | Максимум событий, забранных за tick. |
| `WithMaxAttempts(n)` | 0 (без cap) | Dead-letter строк, у которых attempt-count достигает n. |
| `WithBackoff(base, max)` | 1s, 1h | Per-row exponential retry timing. Передайте `(0, 0)`, чтобы отключить. |
| `WithEventTypeMaxAttempts(map)` | none | Per-event-type override `WithMaxAttempts`. SQL-filter падает, проверка переезжает в Go-dispatch loop. |
| `WithEventTypeBackoff(map[string]BackoffSpec)` | none | Per-event-type override `WithBackoff`. |
| `WithoutListen()` | listen on | Отключить LISTEN/NOTIFY. Polling-only режим. |
| `WithRetention(d)` | off | GC published-строк старше d. |
| `WithGCInterval(d)` | 1h | Cadence retention-sweep'а (no-op без `WithRetention`). |
| `WithLogger(*slog.Logger)` | silent | Debug / Warn / Error на каждый lifecycle-event. |
| `WithMetrics(prometheus.Registerer)` | off | Регистрирует `outbox_events_total{outcome}` (counter), `outbox_publish_duration_seconds` (histogram), `outbox_pending_count` (gauge), `outbox_gc_deleted_total` (counter), `outbox_listen_wakes_total` (counter). |

## Operator helpers

Эндпоинты `/admin` и runbook-скрипты получают три targeted-операции и аггрегированные снапшоты — все работают через любой `db.Querier` (используйте `*db.DB` или передайте `*db.Tx` для атомарных кампаний).

```go
// Принудительный retry конкретного row'а (skip backoff).
err := outbox.RetryNow(ctx, svc.DB, eventID)
// → *errs.Error{Code: outbox_op_not_found} если row нет ИЛИ уже published.

// Re-dispatch уже published events (consumer bug fixed downstream).
n, err := outbox.Replay(ctx, svc.DB, ids...) // missing IDs тихо пропускаются.

// Un-dead-letter row: сбросить attempts, очистить last_error.
err := outbox.ResetAttempts(ctx, svc.DB, eventID)

// Снапшот для /admin dashboard'а — один SELECT с пятью аггрегациями.
s, _ := outbox.GatherStats(ctx, svc.DB)
// Stats{Pending, Eligible, Failed, OldestPending, Published1m}

// Инспектировать топ-N pending / dead-letter rows.
events, _ := outbox.ListPending(ctx, svc.DB, 50)
deads, _ := outbox.ListDead(ctx, svc.DB, 50, /*maxAttempts*/ 5)
```

`Replay` тихо пропускает несуществующие IDs (bulk-операторская семантика); downstream-consumer'ы ДОЛЖНЫ быть idempotent, поскольку Replay перетирает published_at. `RetryNow`/`ResetAttempts` фейлятся loud (`outbox_op_not_found`) если row нет или уже published — runbook не должен молча no-op'ить.

## Per-event-type policy

Когда у одного шумного event_type другие retry-характеристики (flaky upstream → больший cap; audit-log → best-effort 1 attempt), `WithEventTypeMaxAttempts` / `WithEventTypeBackoff` overrides настраивают это per-type:

```go
w, _ := outbox.NewWorker(svc.DB, publishFn,
    outbox.WithMaxAttempts(5),                          // global default
    outbox.WithEventTypeMaxAttempts(map[string]int{
        "billing.charge": 10,   // headroom для flaky payment gateway
        "audit.log":      1,    // best-effort, не piling up
    }),
    outbox.WithEventTypeBackoff(map[string]outbox.BackoffSpec{
        "notif.email": {Base: 30 * time.Second, Max: time.Hour},
    }),
)
```

Когда per-type override активен, SQL-side `attempts < $maxAttempts` filter падает, и dispatch-loop принимает per-type решение в Go. Rows already at their per-type cap silently skipped (no metric tick) — носит та же мощность, что и dead-letter filter на SQL-стороне.

## Error codes

| Code | Возвращается из | Смысл |
|---|---|---|
| `outbox_enqueue_failed` | `Enqueue` | INSERT под капотом зафейлился. Окружающая транзакция ОБЯЗАНА откатиться. |
| `outbox_missing_fields` | `Enqueue` | `Event.EventType` пустой. |
| `outbox_marshal_headers` | `Enqueue` | Headers-map не получилось JSON-кодировать (cyclic / NaN). |
| `outbox_op_not_found` | `RetryNow` / `ResetAttempts` | Row нет ИЛИ уже published. Replay silent на этом — bulk-операторская семантика. |
| `outbox_op_failed` | `RetryNow` / `Replay` / `ResetAttempts` | Underlying UPDATE errored. |
| `outbox_stats_failed` | `GatherStats` | Aggregate-query errored. |
| `outbox_list_failed` | `ListPending` / `ListDead` | SELECT errored. |
| `outbox_worker_nil_db` | `NewWorker` | `NewWorker(nil, fn)`. |
| `outbox_worker_nil_publish_fn` | `NewWorker` | `NewWorker(db, nil)`. |
| `outbox_worker_started` | `Start` | Второй вызов `Start` — worker single-use. |

## Тестирование

`outbox_test.go` запускается против testcontainers Postgres. Покрытые сценарии:

- `Enqueue` вставляет ожидаемую строку.
- `Enqueue` внутри откаченной транзакции НЕ persist'ится (consistency guarantee).
- Worker дренит backlog при tight polling-interval.
- Worker ретраит failed-publish'и; `attempts` строки бампится, и worker в итоге успешен, когда функция возвращает nil.
- `WithMaxAttempts` cap'ит retries — строка остаётся в таблице, но больше не диспатчится.
- `Start` single-use — второй вызов возвращает ошибку.

Запускайте `go test ./db/outbox/...` (пропускается под `-short`).

## См. также

- [`db`](../README.md) — обёртка пула под капотом
- [`clients/natsmap`](../../clients/natsmap/README.md) — типизированный NATS-publish surface; сочетайте с `natsmap.PublishRaw` для outbox-style flow'ов
</content>
