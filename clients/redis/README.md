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
| `WithMetrics(prometheus.Registerer)` | Регистрирует `redis_commands_total{cmd,outcome}`, `redis_command_duration_seconds{cmd}`, `redis_pool_size_total{state}` и `redis_connection_status`. |
| `WithRedisOptions(fn)` | Мутатор `*redis.Options` для single-mode (PoolSize, MinIdleConns, ReadTimeout, TLSConfig). |
| `WithClusterOptions(fn)` | Мутатор `*redis.ClusterOptions` для cluster-mode (`RouteByLatency`, `MaxRedirects` и т.п.). |
| `WithSentinelOptions(fn)` | Мутатор `*redis.FailoverOptions` для sentinel-mode. |
| `WithHook(redis.Hook)` | Дополнительный hook поверх kit observability (OTel tracing, custom audit). Множественные вызовы накапливаются. |
| `WithDefaultTimeout(d)` | Per-command ctx wrap when caller-side deadline is absent. Explicit deadline всегда побеждает. |
| `WithBreaker(*breaker.Breaker)` | Circuit breaker между каждой командой и transport'ом. `redis.Nil` считается успехом; открытое состояние → `*errs.Error{Code: "redis_circuit_open"}`. |

## Cluster + Sentinel modes

```go
// Cluster (production HA через shards)
cli, err := redisclient.ConnectCluster(ctx, redisclient.ClusterConfig{
    Addrs:    []string{"r1:6379", "r2:6379", "r3:6379"},
    Password: cfg.RedisPassword,
}, redisclient.WithClusterOptions(func(o *redis.ClusterOptions) {
    o.RouteByLatency = true
    o.MaxRedirects = 3
}))

// Sentinel (HA failover через master discovery)
cli, err := redisclient.ConnectSentinel(ctx, redisclient.SentinelConfig{
    MasterName:    "mymaster",
    SentinelAddrs: []string{"sentinel1:26379", "sentinel2:26379"},
    Password:      cfg.RedisPassword,
})
```

API остаётся идентичным — `cli.Universal()` возвращает `redis.UniversalClient` (общий interface для всех трёх mode'ов). `cli.Redis()` доступен **только в single-mode** и **panic'ит** под cluster/sentinel — нужен явный `cli.Mode()` branch или сразу `cli.Universal()` в коде, который может крутиться под разными топологиями. `cli.Mode()` репортит текущий topology.

Observability (hook, metrics, breaker, default-timeout) работает одинаково — go-redis маршрутизирует hook через каждый shard.

## Observability

`WithMetrics` устанавливает go-redis Hook, который записывает каждую
команду, выполняемую через `Client.Redis()` / `Client.Universal()` — включая команды
пользовательского кода, не только kit-issued.

- `redis_commands_total{cmd, outcome}` — counter. `outcome="error"`
  исключает `redis.Nil` (sentinel "key not found" — это операционный
  успех).
- `redis_command_duration_seconds{cmd}` — histogram, дефолтные
  бакеты.
- `redis_pool_size_total{state="hits|misses|idle|stale|total"}` —
  gauge, обновляется из `PoolStats()` на каждом scrape (cluster-mode aggregates across shards).
- `redis_connection_status` — gauge `1` после успешного Connect ping'а; `0` после Close. Симметрично с `nats_connection_status`.

## Resilience knobs

```go
cli, _ := redisclient.Connect(ctx, cfg,
    redisclient.WithDefaultTimeout(500*time.Millisecond),  // backstop runaway commands
    redisclient.WithBreaker(redisBreaker),                 // shared breaker
    redisclient.WithHook(otelredis.NewHook()),             // OTel tracing chained AFTER kit hook
)
```

- `WithDefaultTimeout(d)` оборачивает каждый command's ctx в `WithTimeout(d)` только если у caller'а ещё нет deadline. Explicit `context.WithTimeout(...)` побеждает.
- `WithBreaker(*breaker.Breaker)` route'ит каждую команду через `breaker.Execute`. `redis.Nil` — success (не trip'ает breaker). Open-state → `*errs.Error{KindUnavailable, Code: "redis_circuit_open"}` оборачивает `breaker.ErrOpen` — `errors.Is(err, breaker.ErrOpen)` истина.
- `WithHook(redis.Hook)` accumulates user hooks AFTER kit hook. Hook'и НЕ применяются к Ping внутри Connect retry loop — они активны для команд, выпускаемых после возврата Connect.

## Ошибки

| Code | Когда |
|---|---|
| `redis_missing_url` | `Config.URL` пуст на `Connect`. |
| `redis_invalid_url` | `redis.ParseURL` отклонил URL. |
| `redis_connect_failed` | PING зафейлился после исчерпания `ConnectMaxRetries`. Оборачивает последнюю underlying ошибку. |
| `redis_circuit_open` | Команда short-circuit'нута открытым breaker'ом (см. `WithBreaker`). Оборачивает `breaker.ErrOpen`. |

Все три прокидываются как `*errs.Error` с установленным `Kind`:
`KindValidation` для первых двух, `KindUnavailable` для третьей.
Service маппит connect failure в `service_redis_connect_failed`
через `Wrap`.

## См. также

- [`clients/cache`](../cache/README.md) — типизированный кэш Get/Set/SetNotFound/Invalidate поверх любого `*redis.Client`.
- [`service`](../../service/README.md) — `service.Config.Redis` + `WithRedisOptions(...)` для интегрированной проводки.
</content>
