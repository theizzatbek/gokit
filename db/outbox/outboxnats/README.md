# db/outbox/outboxnats

Тонкий adapter, который превращает [`db/outbox`](../README.md) Worker в
NATS publish'er через [`clients/natsmap`](../../../clients/natsmap/README.md).
Заменяет ручной closure из quickstart'а на один вызов.

**Импорт:** `github.com/theizzatbek/gokit/db/outbox/outboxnats`
**Зависит от:** `db/outbox`, `clients/natsmap`, `errs`

## Зачем это нужно

В `db/outbox/README.md` квикстарт показывает каноничный `PublishFn`:

```go
w, _ := outbox.NewWorker(svc.DB, func(ctx context.Context, e outbox.Event) error {
    return natsmap.PublishRaw(ctx, svc.NATSMap, e.EventType, e.Payload, e.Headers)
})
```

Это три строки, которые пишет каждый сервис. Адаптер сводит к одной:

```go
w, _ := outbox.NewWorker(svc.DB, outboxnats.NewPublisher(svc.NATSMap))
```

Логика идентична — байты Payload и Headers проходят как есть; `EventType`
по умолчанию маппится 1:1 на natsmap-овский `publishers[].name`.

## Quickstart

```go
import (
    "github.com/theizzatbek/gokit/db/outbox"
    "github.com/theizzatbek/gokit/db/outbox/outboxnats"
)

w, err := outbox.NewWorker(db, outboxnats.NewPublisher(rt))
if err != nil { return err }
w.Start(ctx)
```

`rt` — это `*natsmap.Runtime`, возвращённый `engine.Build()` (или
`svc.NATSMap` из `service.New`-сборки).

## Маппинг EventType → publisher name

По умолчанию: identity. То есть `Event{EventType: "urlshort.link.created"}`
ищет publisher с YAML-name `urlshort.link.created`.

Override через `WithPublisherNameFn`, когда YAML-имена не совпадают с
EventType-строками (например, namespaced):

```go
outboxnats.NewPublisher(rt,
    outboxnats.WithPublisherNameFn(func(e outbox.Event) string {
        return "bus." + e.EventType
    }),
)
```

Резолвер возвращает пустую строку → `*errs.Error{Code:
"outboxnats_empty_publisher_name"}` без сетевого вызова. Worker
интерпретирует это как обычный publish-fail и перепланирует строку
согласно своему backoff'у.

## Errors

| Code | Когда |
|---|---|
| `outboxnats_empty_publisher_name` | Резолвер вернул `""` (часто = пустой `Event.EventType` под default-резолвером) |
| `natsmap_unknown_publisher` | Резолвенное имя не зарегистрировано в YAML |
| `natsmap_publish_failed` | Транспортная ошибка при `PublishRaw` |

Все три перехватывает outbox.Worker и применяет свой retry-policy на строку.

## Что НЕ делает

- Не кодирует / не декодирует — `Event.Payload` идёт в `PublishRaw` raw bytes.
  Каллер уже сериализовал событие внутри Tx; downstream subscriber декодирует.
- Не переписывает headers — `Event.Headers` мерджится в natsmap-овский
  static-headers слой по тем же правилам, что и при обычном `PublishRaw`.
- Не делает streaming-publish / batched-publish — одно событие = один `PublishRaw`
  вызов. Batched-publish — задача для `batch/` или будущего outbox option'а.

## См. также

- [`db/outbox`](../README.md) — родительский пакет с Worker, Enqueue, retention.
- [`clients/natsmap`](../../../clients/natsmap/README.md) — declarative
  NATS publishers/subscribers через YAML.
