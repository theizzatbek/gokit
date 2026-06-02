# db/inbox/inboxnats

Тонкий wrapper, который превращает domain-handler с сигнатурой
`func(ctx, *db.Tx, Msg[T]) error` в natsmap-совместимый handler с
автоматической дедупликацией через [`db/inbox`](../README.md).

**Импорт:** `github.com/theizzatbek/gokit/db/inbox/inboxnats`
**Зависит от:** `clients/nats`, `db/`, `db/inbox`, `errs/`

## Зачем это нужно

`db/inbox.Process(ctx, db, key, fn)` принимает `fn(*db.Tx) error` — fn пишет
в БД внутри той же Tx, что и inbox row. Но `natsmap.RegisterHandler[T]`
ожидает `func(ctx, Msg[T]) error` — без `*db.Tx`.

`Wrap` мостит две сигнатуры:

```go
natsmap.RegisterHandler[Order](eng, "orders-sink",
    inboxnats.Wrap[Order](
        "orders-svc:order.created",                       // consumer
        svc.DB,
        func(ctx context.Context, tx *db.Tx, m natsclient.Msg[Order]) error {
            return repo.Insert(ctx, tx, m.Data)            // domain Tx work
        },
    ))
```

Под капотом — три шага:

1. Извлечь `event_id` (по умолчанию из `Nats-Msg-Id` header — override через
   `WithEventIDFn`).
2. Вызвать `inbox.Process(ctx, db, Key{consumer, event_id}, fn-wrapper)`.
3. На `OutcomeDuplicate` — return nil без вызова domain fn (natsmap ack'нет
   redelivery, side effect не запустится).

## Quickstart

```go
import (
    natsclient "github.com/theizzatbek/gokit/clients/nats"
    "github.com/theizzatbek/gokit/db"
    "github.com/theizzatbek/gokit/db/inbox/inboxnats"
)

natsmap.RegisterHandler[order](eng, "sink",
    inboxnats.Wrap[order]("svc:order.created", db,
        func(ctx context.Context, tx *db.Tx, m natsclient.Msg[order]) error {
            return repo.Persist(ctx, tx, m.Data)
        }))
```

С captured observability:

```go
in := inbox.New(inbox.Config{Logger: logger, Metrics: promReg})

natsmap.RegisterHandler[order](eng, "sink",
    inboxnats.Wrap[order]("svc:order.created", db, domainFn,
        inboxnats.WithInbox(in),
    ))
```

## Источник event_id

По умолчанию: `headers["Nats-Msg-Id"][0]`. `clients/nats.Publisher` авто-
заполняет этот header UUID'ом, если caller его не set, так что для kit-
managed publish'еров дедупликация работает из коробки.

Когда publisher НЕ ставит `Nats-Msg-Id` (внешние сервисы, custom publisher),
используйте `WithEventIDFn`:

```go
inboxnats.Wrap[order]("svc:x", db, domainFn,
    inboxnats.WithEventIDFn(func(headers map[string][]string, subject string, seq uint64) string {
        // Fallback на JetStream Sequence (стабилен в рамках стрима).
        return subject + ":" + strconv.FormatUint(seq, 10)
    }),
)
```

Резолвер возвращает `""` → handler возвращает `*errs.Error{Code:
"inboxnats_missing_message_id"}` БЕЗ вызова inbox.Process. natsmap пробросит
это в Nak — publisher misconfig станет видимым в alerts вместо silently-
disabled dedup.

## API

```go
const NatsMsgIDHeader = "Nats-Msg-Id"
const CodeMissingMessageID = "inboxnats_missing_message_id"

// EventIDFn resolves a Msg → event id string.
type EventIDFn func(headers map[string][]string, subject string, sequence uint64) string

// DefaultEventIDFn reads NatsMsgIDHeader. Exposed so callers can
// compose it into a fallback chain.
func DefaultEventIDFn(headers map[string][]string, subject string, seq uint64) string

// Wrap converts a (ctx, tx, Msg[T]) function into a natsmap handler
// with inbox dedup.
func Wrap[T any](
    consumer string,
    d *db.DB,
    fn func(ctx context.Context, tx *db.Tx, m natsclient.Msg[T]) error,
    opts ...Option,
) func(ctx context.Context, m natsclient.Msg[T]) error

// Options:
func WithInbox(*inbox.Inbox) Option       // capture observability
func WithEventIDFn(EventIDFn) Option       // override event id resolver
```

## Контракт по результатам

| Исход inbox.Process | Wrap'нутый handler возвращает | natsmap делает |
|---|---|---|
| `OutcomeProcessed`, nil err | nil | Ack |
| `OutcomeDuplicate` | nil | Ack (без вызова domain fn) |
| `OutcomeProcessed`, fn err | wrapped err | Nak → JetStream redelivers |
| Missing message id | `*errs.Error{CodeMissingMessageID}` | Nak |

На `OutcomeProcessed + fn err`: Tx роллбэкается → inbox row не вставляется
→ следующая redelivery входит как новая → fn запускается снова. Это
правильный retry-flow для transient failures.

## Что НЕ делает

- Не encode/decode payload — natsmap codec handle'ит это до того, как
  handler доходит до Wrap.
- Не управляет Ack/Nak напрямую — только error return; natsmap runtime
  переводит nil/err в ack/nak.
- Не предоставляет per-message timeout — caller'у разрешено сделать
  `context.WithTimeout` в domain fn.

## См. также

- [`db/inbox`](../README.md) — родительский пакет с Schema, Process, RetentionWorker.
- [`db/outbox/outboxnats`](../../outbox/outboxnats/README.md) — публикация
  outbox-событий через natsmap (зеркальный adapter).
- [`clients/natsmap`](../../../clients/natsmap/README.md) — declarative
  NATS publishers/subscribers через YAML.
