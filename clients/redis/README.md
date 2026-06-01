# redisclient

Тонкий Redis-bootstrap для kit-based сервисов. Один вызов открывает
`*redis.Client`, ретраит начальный PING с экспоненциальным backoff'ом
и (когда подключено) маршрутит каждую команду через Prometheus +
`*slog.Logger` хук.

**Импорт:** `github.com/theizzatbek/gokit/clients/redis`
**Зависит от:** `github.com/redis/go-redis/v9`

Имя пакета — `redisclient`, чтобы не конфликтовать с собственным
пакетом `redis` у go-redis — та же конвенция, что у `clients/nats` →
`natsclient`.

## Quickstart

```go
cli, err := redisclient.Connect(ctx, redisclient.Config{
    URL:               "redis://localhost:6379",
    ConnectMaxRetries: 5,
},
    redisclient.WithLogger(logger),
    redisclient.WithMetrics(reg),
)
if err != nil { return err }
defer cli.Close()

rdb := cli.Redis()  // *redis.Client — полная go-redis поверхность
```

`service.New` авто-подключает это, когда `service.Config.Redis.URL`
установлен — экспонирует результат как `svc.Redis`.

## Config

| Поле | Env (с префиксом `REDIS_` в service) | Заметки |
|---|---|---|
| `URL` | `REDIS_URL` | Обязательно. `redis://[user:pass@]host:port[/db]`, `rediss://…` для TLS. |
| `ConnectMaxRetries` | `REDIS_CONNECT_MAX_RETRIES` | 0 = no retry (одна попытка). service авто-устанавливает 5 при zero; передайте `-1`, чтобы отключить. |
| `ConnectBackoffBase` | `REDIS_CONNECT_BACKOFF_BASE` | Удваивается на каждой попытке. service инжектит 1s при zero. |
| `ConnectBackoffMax` | `REDIS_CONNECT_BACKOFF_MAX` | Кеп per-attempt wait. service инжектит 16s при zero. |

## Опции

| Опция | Заметки |
|---|---|
| `WithLogger(*slog.Logger)` | Подключает connect-retry Warn + per-command observability через go-redis Hook. |
| `WithMetrics(prometheus.Registerer)` | Регистрирует `redis_commands_total{cmd,outcome}`, `redis_command_duration_seconds{cmd}` и `redis_pool_size_total{state}`. |
| `WithRedisOptions(fn)` | Мутатор для распарсенных `*redis.Options` (PoolSize, MinIdleConns, ReadTimeout, custom TLSConfig — всё, что не выражается в URL). |

## Observability

`WithMetrics` устанавливает go-redis Hook, который записывает каждую
команду, выполняемую через `Client.Redis()` — включая команды
пользовательского кода, не только kit-issued.

- `redis_commands_total{cmd, outcome}` — counter. `outcome="error"`
  исключает `redis.Nil` (sentinel "key not found" — это операционный
  успех).
- `redis_command_duration_seconds{cmd}` — histogram, дефолтные
  бакеты.
- `redis_pool_size_total{state="hits|misses|idle|stale|total"}` —
  gauge, обновляется из `PoolStats()` на каждом scrape.

## Ошибки

| Code | Когда |
|---|---|
| `redis_missing_url` | `Config.URL` пуст на `Connect`. |
| `redis_invalid_url` | `redis.ParseURL` отклонил URL. |
| `redis_connect_failed` | PING зафейлился после исчерпания `ConnectMaxRetries`. Оборачивает последнюю underlying ошибку. |

Все три прокидываются как `*errs.Error` с установленным `Kind`:
`KindValidation` для первых двух, `KindUnavailable` для третьей.
Service маппит connect failure в `service_redis_connect_failed`
через `Wrap`.

## См. также

- [`clients/cache`](../cache/README.md) — типизированный кэш Get/Set/SetNotFound/Invalidate поверх любого `*redis.Client`.
- [`service`](../../service/README.md) — `service.Config.Redis` + `WithRedisOptions(...)` для интегрированной проводки.
</content>
