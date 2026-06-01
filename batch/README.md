# batch

Generic batched-handler диспетчер. Собирает типизированные элементы
через `Submit` и отдаёт их в caller'ский `HandlerFn` срезом — один
вызов на буферный батч. Per-item ack-колбэки позволяют upstream-источникам
(например, kit'овому natsmap pull-подписчику) commit'ить весь батч атомарно.

**Импорт:** `github.com/theizzatbek/gokit/batch`
**Зависит от:** `github.com/prometheus/client_golang/prometheus`, `errs/`

## Когда использовать

- Нужен один вызов handler'а на N элементов + every-item-or-none
  подтверждение.
- Буферизуете event stream в bulk DB-запись
  (`INSERT/UPDATE … VALUES (..), (..), …`).
- Throttling исходящих батчей (один HTTP-push в секунду вместо N
  push'ов в секунду).

Для visit-counter-style агрегации, где вы хотите key-keyed map,
collapsed до flush'а (sum across many events into one row per key),
делайте агрегацию **внутри** `HandlerFn` — это domain-задача, не
kit-задача.

## Quickstart — прямое использование

```go
b, err := batch.New[Event](batch.Config[Event]{
    HandlerFn: func(ctx context.Context, batch []Event) error {
        return persistAll(ctx, batch) // одна транзакция
    },
    BatchSize: 1000,        // обязательно
    Interval:  time.Second, // дефолт 1s при zero
    Logger:    logger,
})
if err != nil { return err }
defer b.Close()

for evt := range events {
    b.Submit(evt, nil) // fire-and-forget
}
```

## Quickstart — с per-item ack

```go
b.Submit(event, func(err error) {
    if err == nil {
        msg.Ack()          // upstream queue commit
    } else {
        msg.Nak()          // redeliver при err
    }
})
```

Каждое `ack`-замыкание в батче срабатывает с одним и тем же `err` —
return value `HandlerFn`. All-or-nothing семантика: успешный батч
ack-подтверждает каждый элемент; неудачный — nak-redeliver'ит каждый.

`natsmap.RegisterBatchedHandler` кита использует именно этот паттерн
внутри, так что YAML-объявленные подписчики получают at-least-once
batched-доставку без того, чтобы пользователи проводили Submit сами.

## Config

| Поле | По умолчанию | Заметки |
|---|---|---|
| `HandlerFn func(ctx, []T) error` | — | **Обязательно.** Получает буферный срез одним вызовом. |
| `BatchSize int` | — | **Обязательно, > 0.** Кап размера; достижение его триггерит ранний flush. |
| `Interval time.Duration` | `1s` | Кап возраста буфера. Любой триггер flush'ит. |
| `Logger *slog.Logger` | nil (silent) | Warn-записи на HandlerFn ошибках. |
| `Metrics prometheus.Registerer` | nil (off) | Четыре `batch_*` коллектора. |

## API

```go
func New[T any](cfg Config[T]) (*Batcher[T], error)

func (b *Batcher[T]) Submit(item T, ack func(err error))
func (b *Batcher[T]) Flush(ctx context.Context) error
func (b *Batcher[T]) Close() error
```

- `Submit` goroutine-safe — производительные горутины вызывают её конкурентно. `nil` ack поддерживается для fire-and-forget элементов.
- `Flush` — ручной дренаж (тесты, интерактивные shutdown-пути).
- `Close` делает один финальный flush, останавливает горутину, идемпотентен.
- `(*Batcher[T])(nil)` безопасен на каждом методе — Submit — no-op, Flush/Close возвращают nil. Позволяет caller'ам пробрасывать опциональный batcher через свой код.

## Trigger-модель

Два триггера работают параллельно:

1. **Interval ticker** — каждый `Interval`.
2. **Size cap** — `Submit` доводит буфер до `BatchSize` различных элементов, non-blocking сигнал прыгает в очередь и триггерит немедленный flush.

Оба зовут один и тот же dispatch-путь; `HandlerFn` запускается **снаружи** batcher-лока, так что high-frequency `Submit` вызовы не stall'ятся на медленном downstream.

## Ошибки

`New` возвращает `*errs.Error` через `errors.Join`, когда обязательные поля отсутствуют — каждый gap всплывает одновременно.

| Code | Cause |
|---|---|
| `batch_missing_handler_fn` | `Config.HandlerFn` nil |
| `batch_invalid_batch_size` | `Config.BatchSize` <= 0 |

## Метрики

Передайте `Config.Metrics = reg`, чтобы зарегистрировать четыре серии:

| Серия | Тип | Labels |
|---|---|---|
| `batch_handlers_total` | Counter | `outcome=success\|error` |
| `batch_items_processed_total` | Counter | — |
| `batch_handler_duration_seconds` | Histogram | — |
| `batch_batch_size` | Histogram | — |

## См. также

- [`clients/natsmap`](../clients/natsmap/README.md) — `RegisterBatchedHandler[T]` подключает паттерн batched-dispatch'а на JetStream Pull-подписчик. YAML конфигурирует `batch_size` и `batch_interval`; кит обрабатывает Submit + ack/nak атомарно.
- `examples/urlshort/internal/links/visit_counter.go` — production use case через natsmap-путь: один срез событий на батч → domain-side агрегация → один UPDATE … FROM (VALUES …).
</content>
