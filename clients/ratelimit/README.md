# clients/ratelimit

Redis-backed sliding-window rate-limiter. Атомарный Lua-скрипт
работает с одним shared budget'ом через N pod'ов, в отличие от
`auth.RateLimit` (in-memory token-bucket — каждый под лимитит
независимо).

**Импорт:** `github.com/theizzatbek/gokit/clients/ratelimit`
**Зависит от:** `clients/redis` + `prometheus/client_golang` + `errs`

## Quickstart

```go
import "github.com/theizzatbek/gokit/clients/ratelimit"

lim, err := ratelimit.NewRedis(svc.Redis, ratelimit.Config{
    KeyPrefix: "rl:login:",
    Limit:     60,
    Window:    time.Minute,
}, ratelimit.WithLogger(svc.Logger()), ratelimit.WithMetrics(reg))
if err != nil { return err }

allow, _ := lim.Allow(ctx, "user:42")
if !allow.Allowed {
    c.Set("Retry-After", strconv.Itoa(int(allow.RetryAfter.Seconds())))
    return errs.RateLimited("too_many", "back off")
}
```

Через `service.WithRateLimit(cfg)` auto-wire'ит лимитер +
регистрирует YAML middleware factory `rate_limit_redis` на Engine.

## Алгоритм

ZSET-based sliding window. На каждый Allow:

1. Drop entries старше `now - window` (`ZREMRANGEBYSCORE`)
2. `ZCARD` → текущее число in-window запросов
3. Если `count >= Limit` → deny + retry-after = `(oldest_ts + window) - now`
4. Иначе → `ZADD now nonce` + `PEXPIRE window` → allow

Атомарность гарантирована Lua-скриптом — два одновременных Allow
с того же ключа не превысят `Limit` ни в одном edge-case'е
(проверено через `TestAllow_ConcurrentExactlyLimitGranted` —
100 goroutines, exactly 10 granted при `Limit=10`).

Nonce (16-hex random) обязателен: без него два запроса в одну
миллисекунду схлопнутся в одну ZSET-entry (score=member конфликт).

## YAML-декларация

`auth/fibermount.MountRateLimitRedisFactory(eng, lim, opts...)`
регистрирует middleware `rate_limit_redis`:

```yaml
groups:
  - prefix: /api
    middleware:
      - { rate_limit_redis: [] }            # IP-keyed (default)
    routes:
      - { method: POST, path: /login, handler: auth.login,
          middleware: [{ rate_limit_redis: [ip, login] }] }   # bucket-suffix "login"
      - { method: POST, path: /transfer, handler: pay.transfer,
          middleware: [{ rate_limit_redis: [user] }] }        # subject-keyed (требует WithRateLimitSubjectKeyFn)
```

| Arg | Default | Назначение |
|---|---|---|
| `args[0]` (strategy) | `ip` | `ip` / `user` (= subject). Для `user` нужен `WithRateLimitSubjectKeyFn(auth.KeyBySubject[C])`. |
| `args[1]` (bucket) | `""` | Опциональный prefix перед извлечённым ключом — позволяет разделять buckets для разных endpoint'ов в рамках одного лимитера. |

Limit + Window берутся из самого лимитера (set'ятся при
`NewRedis`), а не из YAML — YAML декларирует факт применения,
конфигурация знобов — на Go-стороне.

## API-поверхность

| Метод | Возвращает | Заметки |
|---|---|---|
| `NewRedis(rc, cfg, opts...)` | `(*Redis, error)` | Валидация Config'а; nil `rc` → fail-open лимитер для dev'а. |
| `Allow(ctx, key)` | `(Allowance, error)` | Атомарная check-and-increment. Fail-open на ошибке backend'а. |
| `Limit()` | int | Прочитать configured cap. |
| `Window()` | duration | Прочитать configured window. |

`Allowance` carries `Allowed`, `Remaining`, `Limit`, `Window`, `RetryAfter`.

## Опции

| Опция | Заметки |
|---|---|
| `WithLogger(*slog.Logger)` | Warn на Redis-ошибки. |
| `WithMetrics(reg)` | `ratelimit_requests_total{outcome}`, `ratelimit_allow_duration_seconds`, `ratelimit_backend_errors_total`. |

## Fail-open vs fail-closed

Реализация **fail-open** — Redis-blip → Allow=true + warn log +
`ratelimit_backend_errors_total++`. Аргумент: prevention'овый
лимитер, ломающий все writes на time of Redis-outage'а, хуже чем
временно отключённый лимитер.

Для fail-closed: оборачивайте `Allow` своим wrapper'ом и
проверяйте error явно — если не nil → return 503.

## Ограничения

- **Sub-window resolution — миллисекунды.** Sub-ms windows не работают (PEXPIRE и now-math в ms).
- **Один Limit/Window per limiter.** Для multi-bucket budget'ов конструируйте несколько лимитеров.
- **ZSET-memory linear with rate.** При sustained high-traffic ZSET держит до `Limit` entries per key. На миллионы уникальных ключей × миллионы entries — нужен `MEMORY USAGE`-monitoring.

## См. также

- [`auth.RateLimit`](../../auth/README.md) — in-memory token-bucket для single-pod / тестов
- [`auth/fibermount`](../../auth/fibermount/README.md) — YAML-bridge для `rate_limit_redis`
- [`service.WithRateLimit`](../../service/README.md) — auto-wire через service.New
</content>
