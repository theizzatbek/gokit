# db/notify

Goroutine-safe Postgres LISTEN/NOTIFY хелпер. outbox v2 кита использует
тот же паттерн внутри; этот пакет экспонирует его как general-purpose
примитив для широкого набора pub/sub-over-pg use case'ов:

- Broadcast cache invalidation (Postgres-триггер `pg_notify` → app
  дропает локальный кеш).
- Сигналы refresh materialized view.
- Notifications для distributed locks.
- Real-time projection updates.

## Зачем это нужно

Сам LISTEN/NOTIFY требует выделенного соединения, аккуратного
reconnect'а на conn drop'е и handler dispatch loop'а. Каждый сервис,
который это использует, переизобретает их. Этот пакет даёт вам:

- Выделенный pool conn, удерживаемый на всю жизнь notifier'а.
- Bounded-backoff reconnect при conn drop или LISTEN failure.
- Dispatch хендлера в одной горутине в порядке получения.
- Lifecycle `Start` / `Stop`, соответствующий конвенциям кита.

## Quickstart

```go
n := notify.NewNotifier(svc.DB, []string{"cache_invalidate"},
    func(ctx context.Context, n notify.Notification) error {
        cache.Drop(n.Payload)
        return nil
    },
    notify.WithLogger(svc.Logger()),
)
_ = n.Start(ctx)
svc.OnShutdown(n.Stop)

// Где-то ещё (тот же или другой процесс):
_, _ = svc.DB.Exec(ctx, `SELECT pg_notify('cache_invalidate', $1)`, key)
```

## API-поверхность

| Символ | Заметки |
|---|---|
| `NewNotifier(d, channels, handler, opts...)` | Конструкция. Каналы должны быть валидными Postgres-идентификаторами (`[A-Za-z_][A-Za-z0-9_]*`). |
| `(*Notifier).Start(ctx)` | Спавнит listen-горутину. Идемпотентен. |
| `(*Notifier).Stop()` | Отменяет ctx + ждёт горутину. Идемпотентен + nil-safe. |
| `notify.WithLogger(l)` | Подключает slog.Logger для lifecycle + per-notification диагностики. |
| `notify.WithMetrics(reg)` | Регистрирует `notify_notifications_total{channel,outcome}`, `notify_reconnects_total`, `notify_handler_duration_seconds{channel}`. |
| `notify.Notification` | `{Channel, Payload string}` — то, что ваш handler получает на каждом `pg_notify` вызове. |
| `notify.Publish[T](ctx, q, channel, payload)` | Типизированный publisher: JSON-marshal payload → pg_notify. |
| `notify.PublishRaw(ctx, q, channel, payload)` | Low-level publisher: string payload без JSON-обёртки. |

## Publish

`Publish[T]` симметричен Notifier'у на publisher-side. JSON-marshal'ит typed-payload и вызывает `SELECT pg_notify(channel, payload)`:

```go
type CacheBust struct {
    Tenant string `json:"tenant"`
    Key    string `json:"key"`
}

err := notify.Publish(ctx, svc.DB, "cache_bust",
    CacheBust{Tenant: "acme", Key: "config:flags"})

// PublishRaw — для случаев "wake-up sign without payload":
err = notify.PublishRaw(ctx, svc.DB, "outbox_new", "")
```

Channel-name validation — та же что в Notifier (`[A-Za-z_][A-Za-z0-9_]*`). Empty/unsafe channel → `*errs.Error{Code: notify_invalid_channel}`. Errors map'ятся в `notify_encode_failed` (JSON marshal failure) / `notify_publish_failed` (pg_notify SQL error). Publish принимает любой `db.Querier`, так что может работать внутри tx — pg_notify буфферится до COMMIT, subscriber видит только после durable persist.

## Семантика

- **Изоляция соединения**: notifier держит ОДИН `*pgxpool.Conn` на
  весь срок жизни. Рекомендуется `MaxConns >= 2` пула, чтобы
  foreground-запросы не голодали.
- **Без durability**: NOTIFY — fire-and-forget. Notifications,
  отправленные в окно reconnect, ТЕРЯЮТСЯ. Caller'ы, которым нужна
  durability, должны сочетать это с recovery-механизмом — например,
  SELECT по индексной таблице при reconnect'е, чтобы дренить
  пропущенное.
- **Single-goroutine handler**: notifications dispatch'атся в
  порядке получения, по одной за раз. Блокирующий handler queue'ит
  последующие notifications в server-side буфере. Fan out в worker
  pool изнутри handler'а для high-throughput источников.
- **Ошибки хендлера**: логируются на Warn (когда WithLogger
  установлен) и игнорируются. У Postgres нет nak/redeliver примитива
  — оператор должен инструментировать retry отдельно.

## Сравнение с `db/outbox`

outbox использует LISTEN/NOTIFY внутри для wake-up пути worker'а.
Эти два куска служат разным нуждам:

| | `db/outbox` | `db/notify` |
|---|---|---|
| **Durability** | At-least-once через Postgres-строки. | Fire-and-forget — нет DB-state. |
| **Use case** | Транзакционный event publish на реальную шину (NATS / Kafka). | App-internal real-time сигналы. |
| **Sender** | `outbox.Enqueue` внутри бизнес-Tx. | Plain `pg_notify(channel, payload)` откуда угодно. |

Оба могут сосуществовать — outbox использует свой канал
(`outbox_new`), отличный от чего-либо, что вы зарегистрируете через
`notify.NewNotifier`.

## См. также

- [`db`](../README.md) — обёртка пула под капотом.
- [`db/outbox`](../outbox/README.md) — durable transactional outbox; использует тот же LISTEN-паттерн внутри.
- Postgres-документация по [LISTEN](https://www.postgresql.org/docs/current/sql-listen.html) / [NOTIFY](https://www.postgresql.org/docs/current/sql-notify.html).
</content>
