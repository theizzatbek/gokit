# db/inbox

Consumer-side companion to [`db/outbox`](../outbox/README.md). Converts
at-least-once pub/sub delivery в *effectively-once* processing, храня
`(consumer, event_id)` строку **в той же транзакции**, что и domain-state.

**Импорт:** `github.com/theizzatbek/gokit/db/inbox`
**Зависит от:** `db/`, `errs/`, `prometheus/client_golang` (опционально)

## Зачем это нужно

At-least-once delivery (NATS JetStream, Kafka, любой pub/sub с retry'ями)
значит consumer'ы видят одно и то же сообщение **≥1 раз**. Без дедупликации
дубликат запускает side-effect дважды — заплатить пользователю два раза,
послать email два раза, создать запись два раза.

Inbox-таблица — стандартный ответ:

- `PRIMARY KEY (consumer, event_id)` — вставка идёт **внутри той же Tx**,
  что и domain-работа.
- Вторая delivery натыкается на UNIQUE-constraint, Process возвращает
  `OutcomeDuplicate` без вызова `fn`.
- DB-state гарантированно exactly-once. Side-effects вне DB (третьи API)
  — задача для собственных idempotency-ключей.

Симметрично outbox'у:

|  | Direction | Гарантия |
|---|---|---|
| `db/outbox` | publish-side | "Я commit'нул state — publish durable." |
| `db/inbox` | consumer-side | "Я получил publish — state durable И не сделаю дважды." |

## Quickstart

```go
import "github.com/theizzatbek/gokit/db/inbox"

// 1. Применить DDL (идемпотентно — безопасно на каждом старте).
if _, err := svc.DB.Exec(ctx, inbox.Schema()); err != nil { return err }

// 2. Внутри handler'а: Process(ctx, db, Key, fn).
outcome, err := inbox.Process(ctx, svc.DB, inbox.Key{
    Consumer: "orders-svc:link.created",
    EventID:  msg.Headers.Get("Nats-Msg-Id"),
}, func(tx *db.Tx) error {
    return persistOrder(ctx, tx, evt)
})
if err != nil { return err }
// outcome == OutcomeDuplicate тоже success — просто ack'ать redelivery.
```

С observability:

```go
in := inbox.New(inbox.Config{
    Logger:  logger,
    Metrics: promReg,
})
outcome, err := in.Process(ctx, svc.DB, key, fn)
```

## API

```go
// Schema returns idempotent DDL.
func Schema() string

// Key uniquely identifies one message-to-consumer pair.
type Key struct {
    Consumer string  // e.g. "orders-svc:link.created"
    EventID  string  // e.g. Nats-Msg-Id, outbox UUID, Kafka key
}

// Outcome — результат Process'а.
type Outcome int
const (
    OutcomeProcessed Outcome = iota  // fn ran, Tx committed
    OutcomeDuplicate                 // row existed, fn did NOT run
)

// Package-level: без logger/metrics.
func Process(ctx, *db.DB, Key, fn func(*db.Tx) error) (Outcome, error)

// С captured observability.
func New(Config) *Inbox
func (*Inbox) Process(ctx, *db.DB, Key, fn func(*db.Tx) error) (Outcome, error)

// Bulk-API: один tx на весь батч.
func ProcessBatch(ctx, *db.DB, []Key, fn func(*db.Tx, newIdx []int) error) ([]Outcome, error)
func (*Inbox) ProcessBatch(...) ([]Outcome, error)

// Auxiliary: проверка / mark без fn.
func Exists(ctx, Querier, Key) (bool, error)
func MarkProcessed(ctx, Querier, Key) (Outcome, error)

// RetentionWorker для периодической чистки.
func NewRetentionWorker(*db.DB, RetentionConfig) (*RetentionWorker, error)
func (*RetentionWorker) Start(ctx context.Context)
func (*RetentionWorker) Stop()
func (*RetentionWorker) Tick(ctx context.Context) (rowsDeleted int64, err error)
```

## Bulk: `ProcessBatch`

Single round-trip dedupe для NATS pull-subscription'ов и батчей с десятками-сотнями messages. Один INSERT с UNNEST + ON CONFLICT DO NOTHING + RETURNING; `fn` получает индексы новых keys в исходном слайсе:

```go
msgs := sub.Fetch(50, maxWait)
keys := make([]inbox.Key, len(msgs))
for i, m := range msgs {
    keys[i] = inbox.Key{Consumer: "orders:link.created", EventID: m.Header.Get("Nats-Msg-Id")}
}

outcomes, err := inbox.ProcessBatch(ctx, svc.DB, keys, func(tx *db.Tx, newIdx []int) error {
    var newPayloads []LinkCreated
    for _, i := range newIdx {
        newPayloads = append(newPayloads, decoded[i])
    }
    return persistAll(ctx, tx, newPayloads)
})

// Ack KAŽDOЕ msg — duplicates безопасны для ack'а, как и для single-Process.
for i, m := range msgs {
    _ = m.Ack()
    _ = outcomes[i] // OutcomeProcessed | OutcomeDuplicate
}
```

`fn err → rollback`: ни insert'ы, ни side-effects не персистятся; redelivery пытается заново. `keys` пустой → `*errs.Error{Code: inbox_batch_empty}`. Каждый key валидируется до открытия tx (loud-fail на первом плохом). Repeated keys внутри одного батча: первая позиция Processed, остальные Duplicate.

## Auxiliary: `Exists` + `MarkProcessed`

```go
// Pure check, без INSERT.
exists, _ := inbox.Exists(ctx, svc.DB, key)

// Record receipt без вызова handler'а — для случаев, когда side-effect
// уже произошёл externally (например, third-party API подтвердил доставку).
outcome, _ := inbox.MarkProcessed(ctx, svc.DB, key)
// → OutcomeProcessed (новый) или OutcomeDuplicate (already recorded)
```

## Algorithm

```
Process(ctx, db, Key{C, ID}, fn)
        │
        ▼
  db.Tx(ctx, func(tx) {
      tag := INSERT INTO inbox (consumer, event_id) VALUES (C, ID)
             ON CONFLICT DO NOTHING
      if tag.RowsAffected() == 0:
          outcome = Duplicate            ← row уже существовал
          return nil                      ← пустая COMMIT (no-op)
      outcome = Processed
      return fn(tx)                       ← domain Tx
  })
```

Один SQL statement, один Tx — атомарность гарантирована, никакой
`SERIALIZABLE` не нужен. Если `fn` возвращает err, Tx роллбэкается, и
inbox-row **не вставляется** — следующая redelivery успешно запустит `fn`.

## Race contract

Две параллельные delivery'и того же `(consumer, event_id)`:

1. Обе зашли в `db.Tx`, обе сделали `INSERT ... ON CONFLICT DO NOTHING`.
2. Один Tx выигрывает (вставляет row + получает RowsAffected=1).
3. Второй получает RowsAffected=0 → возвращает `OutcomeDuplicate` **немедленно**.

Loser **НЕ ждёт** commit'а winner'а — если winner ещё не commit'нул, loser
acks "на веру", но broker'овская следующая redelivery увидит уже
зафиксированную row. v1 принимает этот trade-off; v2 может добавить
`WithRaceWait()` через advisory-lock для strict serialization.

Tested: 50 goroutines с одинаковым Key → ровно 1 Processed, 49 Duplicate, fn
вызвалась **ровно один раз** (`TestProcess_RaceExactlyOneProcessed`).

## RetentionWorker

Inbox rows — receipts, и без пруна они растут навсегда. `RetentionWorker`
DELETE'ит rows старше TTL на каждом тике:

```go
w, err := inbox.NewRetentionWorker(svc.DB, inbox.RetentionConfig{
    TTL:      30 * 24 * time.Hour,   // обязательно — нет дефолта
    Interval: 1 * time.Hour,         // обязательно
    Logger:   logger,
    Metrics:  promReg,
})
if err != nil { return err }
w.Start(ctx)
defer w.Stop()
```

**Multi-replica deployment**: каждая реплика, запустившая worker, делает
DELETE — PG handles concurrent DELETE'ы корректно, но дублированная работа
тратит CPU. Варианты:

- Запустить worker только на одной реплике ("admin pod" паттерн).
- Использовать `db/lock` (leader-elect) вокруг `Tick` самостоятельно.

v1 не делает leader-elect автоматически — это open question в spec'е.

`Tick(ctx)` — синхронный one-shot prune для тестов и manual maintenance.
Безопасно вызывать после `Stop()`.

## Errors

| Code | Когда |
|---|---|
| `inbox_missing_consumer` | `Key.Consumer == ""` |
| `inbox_missing_event_id` | `Key.EventID == ""` |
| `inbox_tx_failed` | INSERT или fn упали — Tx роллбэкается, Cause обёрнут |
| `inbox_batch_empty` | `ProcessBatch` вызван с пустым slice — loud-fail вместо silent no-op в tx |
| `inbox_invalid_retention_ttl` | `RetentionConfig.TTL ≤ 0` |
| `inbox_invalid_retention_interval` | `RetentionConfig.Interval ≤ 0` |
| `inbox_retention_tick_failed` | DELETE упал во время `Tick` |

`fn`'s error пробрасывается обёрнутым в `inbox_tx_failed` — `errors.Is`
работает через цепочку Unwrap, так что caller может проверить свой
domain-error type.

## Observability

### slog

- `Debug "inbox process"` — outcome + duration + consumer + event_id.
- `Warn "inbox process failed"` — на любой Tx-fail.
- `Info "inbox retention tick"` — rows_deleted + duration.
- `Warn "inbox retention tick failed"` — на DELETE-fail.

### Prometheus

| Метрика | Тип | Labels |
|---|---|---|
| `inbox_processed_total` | Counter | `consumer`, `outcome` (`processed` / `duplicate` / `error`) |
| `inbox_process_duration_seconds` | Histogram (DefBuckets) | `consumer` |
| `inbox_retention_rows_deleted_total` | Counter | — |
| `inbox_retention_tick_duration_seconds` | Histogram | — |
| `inbox_retention_tick_errors_total` | Counter | — |

## Side effects outside the DB

Inbox гарантирует exactly-once **DB-state**. НЕ гарантирует exactly-once для:

- Третьих HTTP API (платежки, email-сервисы) — используйте per-request
  Idempotency-Key (`fibermap.IdempotencyKey` / `clients/cache/idempotency`).
- File I/O, OS-вызовы — собственная idempotency.
- Метрики/логи — обычно дешевле послать дважды, чем гарантировать exactly-once.

Если все side-effects идут через `db.Tx`, inbox даёт exactly-once на
end-to-end pipeline.

## См. также

- [`db/outbox`](../outbox/README.md) — publish-side mirror.
- [`db/inbox/inboxnats`](inboxnats/README.md) — natsmap handler wrapper
  (отдельный PR; делает Process из коробки на `Nats-Msg-Id`).
- [`db/lock`](../lock/README.md) — leader-elect для multi-replica retention.
- [`fibermap/idempotency.go`](../../fibermap/idempotency.go) — HTTP-edge
  idempotency-key paddern (другая ось).
