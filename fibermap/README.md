# fibermap

YAML-декларативный роутер и middleware-композер для [Fiber v2](https://github.com/gofiber/fiber). Описываете роуты в YAML, регистрируете хендлеры и middleware по имени (никакой рефлексии), получаете типизированный per-request контекст и отгружаете сервис через `Run()`, который в коробке содержит recover + request-id + slog + Prometheus + healthz.

**Импорт:** `github.com/theizzatbek/gokit/fibermap`
**Зависит от:** `gofiber/fiber/v2`, `gopkg.in/yaml.v3`, `prometheus/client_golang`, `github.com/theizzatbek/gokit/errs`, `github.com/theizzatbek/gokit/fibermap/bind`

## Зачем это нужно

Hand-rolled Fiber-bootstrap означает заново-решать routes-vs-code-coupling, middleware-ordering, OpenAPI-интеграцию, panic-recovery, метрики, healthcheck'и и graceful shutdown на каждом сервисе. fibermap переносит всё это в один декларативный файл (routes.yaml) + build-once-mount-once `Engine[T]` configurator. Три вещи пронизывают пакет:

1. **Lifecycle enforced.** `New[T]() → SetContextBuilder → Register* → LoadFile → Run` (или `Mount`). `Mount` валидирует всё вместе и возвращает `errors.Join` всех проблем; ничто не устанавливается частично.
2. **Per-request `Context[T]` строится один раз и пропагируется.** Хендлеры видят `*Context[T]`, где `T` — это ваш `AppCtx`, несущий request-scoped data (user_id, request_id, logger). Один ContextBuilder, потом каждый хендлер читает типизированный state.
3. **Middleware-цепочки резолвятся декларативно.** Группы `middleware_set` + per-route `middleware:`-списки. Plain (`bearer`) и factory (`require_role: [admin]`) формы; sets рекурсивно расширяются; дубликаты deduped'ятся по `(name, args)`.

## Quickstart

`routes.yaml`:

```yaml
groups:
  - prefix: /v1
    routes:
      - method: GET
        path: /ping
        handler: ping
        name: ping.get
```

`main.go`:

```go
package main

import (
    "github.com/gofiber/fiber/v2"
    "github.com/theizzatbek/gokit/fibermap"
)

type AppCtx struct {
    UserID string
}

func main() {
    eng := fibermap.New[AppCtx]()
    eng.SetContextBuilder(func(c *fiber.Ctx) (AppCtx, error) {
        return AppCtx{}, nil
    })

    fibermap.RegisterHandler(eng, "ping", func(c *fibermap.Context[AppCtx]) error {
        return c.SendString("pong")
    })

    if err := eng.Run(fibermap.WithAddr(":3000")); err != nil {
        panic(err)
    }
}
```

Вот и всё. Бандл `Run` даёт вам `/healthz`, `/metrics`, request-id, структурированные access-логи и panic-recovery бесплатно. Используйте `fibermap.Default[T]()` вместо `New[T]()`, чтобы также встроить ops-бандл в сам engine (авто-применяется даже в тестах через `Mount`).

## Конфигурация

### Сборка engine'а

| Функция | Что даёт |
|---|---|
| `New[T]() *Engine[T]` | bare-engine; вы opt in'ите в каждую фичу |
| `Default[T]() *Engine[T]` | engine с `WithRecover` + `WithRequestLogger` + `WithMetrics` + `WithHealthCheck` defaults pre-applied к Run |

### Методы setup engine'а

| Метод | Когда вызывать |
|---|---|
| `SetContextBuilder(fn ContextBuilder[T])` | Обязательно. Строит per-request `Context[T].Data` из `*fiber.Ctx` (читать Bearer-locals, request-id и т.д.) |
| `SetValidator(v bind.Validator)` | Опционально. Используется `RegisterHandlerWithBody/Query/Params/Headers` для валидации декодированных struct'ов |
| `SetCacheDefaults(d CacheDefaults[T])` | Опционально. KeyBy / Headers / Control defaults для YAML-блока `cache:` |
| `SetBindErrorHandler(fn BindErrorFunc[T])` | Опционально. Кастомный error-mapping для bind-failures (по умолчанию: `errs.Validation`) |

### Опции `Run` (RunOption)

| Опция | По умолчанию | Заметки |
|---|---|---|
| `WithAddr(":3000")` | из env `$PORT`, иначе `:3000` | TCP listen-адрес |
| `WithRoutesPath("routes.yaml")` | `routes.yaml` | Путь, передаваемый во внутренний `LoadFile`, если вы пропустили ручной `LoadFile` |
| `WithRoutesFS(fs.FS)` | none | `embed.FS` источник — встраивает routes.yaml в бинарь |
| `WithFiberConfig(fiber.Config)` | минимальный | Кастомный `*fiber.App` config (override `ErrorHandler`, `BodyLimit` и т.д.) |
| `WithUse(handlers ...fiber.Handler)` | `[RequestID]` | Fiber-level middleware, устанавливаемый ДО engine'овского contextInit |
| `WithConfigureApp(fn func(*fiber.App))` | none | Хук для манипуляции `*fiber.App` после Mount |
| `WithShutdownTimeout(d)` | 10s | Graceful shutdown deadline на SIGINT/SIGTERM |
| `WithoutSignalHandling()` | — | Пропустить built-in signal-handler (caller управляет shutdown'ом) |
| `WithRecover(logger)` / `WithoutRecover()` | on (slog.Default) | Panic-recovery со стек-трейсом |
| `WithoutRequestID()` | request-id on | Инжектит `X-Request-ID` |
| `WithRequestLogger(logger, skipPaths...)` / `WithoutRequestLogger()` | on (пропускает `/healthz`,`/metrics`) | Структурированный access-лог |
| `WithMetrics(path)` / `WithoutMetrics()` | `/metrics` (только через `Default[T]`) | Prometheus endpoint |
| `WithMetricsRegistry(reg)` | приватный registry | Route middleware + scrape через caller-provided registry — унифицирует `fibermap_http_*` с собственными коллекторами app'а. |
| `WithHealthCheck(path)` / `WithoutHealthCheck()` | `/healthz` | Always-200 health endpoint, обходит ContextBuilder |
| `WithReadiness(path, checkers...)` | off | Авто-probed readiness endpoint — запускает каждый `Checker` параллельно, 200 `{"status":"ok"}` или 503 `{"status":"degraded","checks":{…}}`. Обходит ContextBuilder. |
| `WithReadinessOpts(opts...)` | none | Прокидывает `[]ReadinessOption` (например, `WithReadinessTimeout(d)`) в авто-установленный readiness handler. |

## Common patterns

### YAML-схема полного роута

```yaml
groups:
  - prefix: /api/v1
    middleware:                       # group-level: применяется к каждому вложенному роуту + sub-group
      - bearer: []                    # factory middleware: map-форма даже с пустыми args
    groups:                           # вложенные группы наследуют middleware
      - prefix: /tasks
        routes:
          - method: GET
            path: ""                  # пусто = сам group-prefix
            handler: tasks.list       # имя, зарегистрированное через RegisterHandler
            name: tasks.list          # обязательно: стабильное имя для OpenAPI / Routes()
            tags: [tasks]             # опционально: openapi tag(s)
            summary: List tasks
            description: List the caller's tasks
            middleware:               # route-level: добавляется ПОСЛЕ group-middleware
              - require_role: [admin]
            timeout: 5s               # опционально: per-route timeout
            cache:                    # опционально: response-cache
              ttl: 10s
              control: true           # уважать Cache-Control: no-store
              headers: true           # cache + replay response-headers
```

### Типизированный body-bound хендлер

```go
type CreateTaskRequest struct {
    Title string `json:"title" validate:"required,min=1,max=200"`
}

type Task struct {
    ID    string `json:"id"`
    Title string `json:"title"`
}

fibermap.RegisterHandlerWithBody(eng, "tasks.create",
    func(c *fibermap.Context[AppCtx], req CreateTaskRequest) error {
        t := svc.Create(c.Data.UserID, req.Title)
        return c.Status(201).JSON(t)
    },
    fibermap.WithResponse(201, Task{}),     // для OpenAPI
    fibermap.WithResponse(400, errs.Response{}),
)
```

Sibling-хелперы: `RegisterHandlerWithQuery`, `RegisterHandlerWithParams`, `RegisterHandlerWithHeaders`. Все прогоняют `eng.validator` против декодированного struct'а перед вызовом хендлера. Bind-failures всплывают как `*errs.Error{Kind: Validation}`, маппящееся в 400.

### Комбинированные binder'ы — `RegisterHandlerWithInput`

Когда один эндпоинт нуждается более чем в одном из `{body, params, query, headers}`
типизированных вместе — например, PATCH /things/:id с body, path id и query
filter — используйте `RegisterHandlerWithInput`. Input-struct объявляет любую
комбинацию полей именами строго `Body`, `Params`, `Query`, `Headers`:

```go
type UpdateThingInput struct {
    Body   UpdateBody       // {"title": "...", "tags": [...]}
    Params struct {         // /things/:id
        ID string `params:"id" validate:"required,uuid"`
    }
    Query struct {          // ?notify=true
        Notify bool `query:"notify"`
    }
}

fibermap.RegisterHandlerWithInput(eng, "things.update",
    func(c *fibermap.Context[AppCtx], in UpdateThingInput) error {
        // in.Body, in.Params, in.Query уже распарсены + валидированы.
        return c.JSON(svc.Update(in.Params.ID, in.Body, in.Notify))
    })
```

Кит рефлектит Input **один раз на регистрации**, строит binder-список и
переиспользует его per request — никакого cost'а рефлексии на hot path'е
сверх field-index lookup'а на каждое recognised-поле. Поля с именами вне
зарезервированного набора игнорируются.

Каждое recognised-поле авто-прикрепляет своё matching `With*`-опцию, так
что генерация OpenAPI видит полный набор схем без того, чтобы caller
пробрасывал какие-либо opts. Валидация проходит через `eng.validator`
точно так же, как для single-source вариантов.

**Misuse panic'ит на регистрации:**
- `Input` не struct.
- Нет recognised-поля (используйте plain `RegisterHandler`).
- recognised-поле, тип которого не struct.

### Factory middleware (параметризуемые)

```go
// На registration-time
fibermap.RegisterMiddlewareFactory(eng, "require_role",
    func(args []string) (fibermap.MiddlewareFunc[AppCtx], error) {
        roles := args
        return func(c *fibermap.Context[AppCtx]) error {
            if !slices.Contains(roles, c.Data.Role) {
                return errs.Permission("forbidden", "missing required role")
            }
            return c.Next()
        }, nil
    })

// В routes.yaml
middleware:
  - require_role: [admin]
  - require_role: [editor, owner]   # разные args = отдельный handler в dedup-кеше
```

### Mount на существующий *fiber.App (тесты + композируемость)

```go
app := fiber.New(fiber.Config{
    ErrorHandler: fibermap.ErrorHandler(logger),  // подключает errs.HTTP в Fiber
})
app.Use(authObj.Bearer(auth.BearerOptional))      // pre-engine middleware
if err := eng.Mount(app); err != nil {
    return err
}
resp, _ := app.Test(httptest.NewRequest("GET", "/v1/ping", nil), -1)
```

`Mount` — это единственный способ использовать engine без `Run` — нужен для in-process тестов с `app.Test`.

### Программные роуты (сырые Fiber-хендлеры)

```go
eng.Add("POST", "/auth/refresh", "auth.refresh",
    func(c *fibermap.Context[AppCtx]) error {
        return authObj.IssueRefresh(c.Ctx)  // оборачивает сырой *fiber.Ctx хендлер
    })
```

Программные роуты участвуют в генерации OpenAPI и engine-овой цепочке ContextBuilder + middleware. Они не могут нести YAML-middleware (используйте `WithUse` или wrap руками).

## Error-модель

Каждая ошибка, возвращаемая библиотекой — `*fibermap.Error` (alias вокруг собственного типизированного error-типа пакета) со `Stage` (`parse` / `mount` / `register`) и `Code*`-константой. Новые error-условия добавляют `Code*`-константу. Mount-stage ошибки аккумулируются в один `errors.Join`, так что все проблемы всплывают в одном вызове.

Используйте `fibermap.ErrorHandler(logger)` как `fiber.Config.ErrorHandler`, чтобы подключить `errs.HTTP` для handler-ошибок и fallback'нуться на собственный код `*fiber.Error` для router-level (404/405) ошибок. Авто-логирует 5xx через переданный логгер; 4xx silent по умолчанию.

## Observability

`fibermap.Default[T]()` (или `Run` с matching-опциями) shipping'ит:

- **slog access-лог** с method, path, status, duration_ms, request_id, response_size
- **Prometheus метрики** на `/metrics` — `http_requests_total{method,path,status}`, `http_request_duration_seconds`, in-flight gauge
- **Health endpoint** на `/healthz` — обходит ContextBuilder, так что работает, даже когда auth/db лежит
- **Readiness endpoint** (опционально через `WithReadiness`) — запускает переданные `Checker`'ы параллельно под shared deadline'ом; 200 `{"status":"ok"}` когда каждая проверка проходит, 503 `{"status":"degraded","checks":{name:err}}` иначе. `db`, `clients/nats`, `clients/redis` shipping'ят `NewChecker(client, name)` адаптеры.
- **Request ID** пропагируется как `X-Request-ID` header + хранится в `c.Locals(fibermap.LocalsRequestID)`

Передайте `*slog.Logger` в `WithRecover`, `WithRequestLogger`. nil = `slog.Default()`.

## Idempotency-Key

`fibermap.IdempotencyKey(store, opts...)` возвращает Stripe-style idempotency-middleware: для unsafe-методов (POST/PUT/PATCH/DELETE) первый ответ, keyed by `X-Idempotency-Key`, capture'ится в pluggable `IdempotencyStore` и replay'ится дословно на последующие запросы с тем же ключом. Replay'и несут `X-Idempotent-Replay: true`, чтобы клиенты (и APM-трассы) могли их отличать.

```go
store := cache.NewIdempotencyStore(svc.Redis, "idem:payments:")
app.Post("/payments",
    fibermap.IdempotencyKey(store,
        fibermap.WithIdempotencyTTL(48*time.Hour),
        fibermap.WithIdempotencyRequired(),
    ), createPayment)
```

| Опция | По умолчанию | Заметки |
|---|---|---|
| `WithIdempotencyHeader(name)` | `X-Idempotency-Key` | Кастомный inbound-header. |
| `WithIdempotencyTTL(d)` | 24h | Сколько replay'и остаются доступными. |
| `WithIdempotencyMethods(...)` | POST/PUT/PATCH/DELETE | Сузить / расширить кешируемый method-set. |
| `WithIdempotencyMaxBodySize(n)` | 1 MiB | Oversize-ответы проходят без кеширования. |
| `WithIdempotencyRequired()` | off | Missing header возвращает 400 с `idempotency_key_missing`. |
| `WithIdempotencySkipStatus(...)` | 5xx | Status-коды, которые НЕ должны кешироваться. |
| `WithIdempotencyLockTTL(d)` | 30s | TTL SETNX-лока вокруг in-flight-хендлера (когда store реализует `IdempotencyLocker`). |
| `WithIdempotencyWithoutLock()` | off | Подавить lock-path даже если store реализует `IdempotencyLocker`. |

Default cache-backend — `clients/cache.NewIdempotencyStore(svc.Redis, prefix)`, который реализует и `IdempotencyStore`, и `IdempotencyLocker` (SETNX). YAML-роуты подключают factory через `auth/fibermount.MountIdempotencyKeyFactory(eng, store)` и используют `idempotency_key: ["1h", "required"]` в `routes.yaml`.

### Concurrency-lock

Когда store реализует `IdempotencyLocker` (например, `cache.RedisIdempotencyStore`), middleware автоматически берёт короткоживущий SETNX-лок на in-flight-хендлер:

1. На cache-miss → `AcquireLock(ctx, key, lockTTL)`.
2. Если lock уже занят другим request'ом → **409 Conflict** с Code `idempotency_in_flight`.
3. После завершения хендлера → `ReleaseLock` (через defer, даже на error).

Lock-TTL (default 30s) защищает от зависшего/упавшего хендлера — лок expires автоматически. Подавить через `WithIdempotencyWithoutLock()` если concurrent-409 шум перевешивает risk дублирующего handler-run'а (например, для очень долгих хендлеров).

Stores, которые НЕ реализуют locker, сохраняют pre-lock-поведение — два concurrent-запроса с одним ключом могут оба запустить хендлер, downstream-идемпотентность (DB unique constraints / transactional outbox) обязательна.

## Request-scoped logger

`fibermap.LoggerInjector(base)` — это Fiber-middleware, который выводит per-request `*slog.Logger` из `base` и хранит его под `LocalsLogger`. Хендлеры читают его обратно через `fibermap.LoggerFrom(c)` и получают логгер pre-bound с:

- `method` (HTTP-метод)
- `path` (routed-pattern)
- `request_id` (когда middleware `RequestID` запускался ранее)
- `user_id` (когда `LocalsAuthSubject` populate'д — пакет `service` кита авто-подключает это из JWT-principal'а)
- `route` (когда у роута установлен `.Name(...)`)

```go
app.Use(fibermap.RequestID())
app.Use(fibermap.LoggerInjector(svc.Logger()))

app.Post("/links", func(c *fiber.Ctx) error {
    fibermap.LoggerFrom(c).Info("link created", "code", code)
    // эмитит {... method=POST path=/links request_id=... user_id=... code=...}
    return c.SendStatus(201)
}).Name("links.create")
```

`service.New` авто-устанавливает `LoggerInjector` на уровне App; opt out через `service.WithoutLoggerInjector()`, когда вы подключаете свой.

`LoggerFrom(nil)` и `LoggerFrom(c)` без установленного middleware оба fallback'ятся на `slog.Default()`, так что handler-код остаётся panic-free в любом контексте проводки.

## Security headers

`fibermap.SecurityHeaders(opts...)` возвращает Fiber-middleware, который добавляет OWASP-baseline response-headers — `Strict-Transport-Security`, `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: strict-origin-when-cross-origin` и API-friendly `Content-Security-Policy`. Mount на уровне App, чтобы `/metrics`, `/healthz`, `/readyz` тоже несли headers:

```go
app.Use(fibermap.SecurityHeaders(
    fibermap.WithHSTSIncludeSubdomains(),
    fibermap.WithCSP("default-src 'self'; script-src 'self'"),
))
```

| Опция | Эффект |
|---|---|
| `WithHSTSMaxAge(seconds)` | Override default 1-year `max-age` |
| `WithHSTSIncludeSubdomains()` | Append `includeSubDomains` — каждый sub-domain становится HTTPS-only |
| `WithHSTSPreload()` | Append `preload` (валидно только с includeSubdomains + после регистрации в hstspreload.org) |
| `WithoutHSTS()` | Дропнуть HSTS-header (для non-HTTPS деплоев) |
| `WithCSP(policy)` | Override default API-friendly CSP |
| `WithoutCSP()` | Дропнуть CSP-header |
| `WithFrameOptions(value)` | Override `X-Frame-Options` (default `DENY`) |
| `WithReferrerPolicy(value)` | Override `Referrer-Policy` (default `strict-origin-when-cross-origin`) |

`service.New` авто-устанавливает middleware с defaults; передайте `service.WithoutSecurityHeaders` или `service.WithSecurityHeaders(opts...)`, чтобы отключить или кастомизировать.

## Тестирование

Используйте `fibermap/fibermaptest` для assertion'ов над `Engine.Routes()` (route-inventory проверки). Для request-level тестов используйте `Engine.Mount(app)` на свежем `*fiber.App` и драйвите `app.Test(req)`.

```go
func TestRoutes(t *testing.T) {
    eng := buildEngine(t)                    // ваш setup
    app := fiber.New(fiber.Config{ErrorHandler: fibermap.ErrorHandler(nil)})
    if err := eng.Mount(app); err != nil { t.Fatal(err) }

    resp, _ := app.Test(httptest.NewRequest("GET", "/v1/ping", nil), -1)
    require.Equal(t, 200, resp.StatusCode)
}
```

## Ограничения

- **Нет built-in rate-limiting'а.** Используйте `gofiber/fiber/v2/middleware/limiter` через `WithUse`.
- **Нет hot-reload routes.yaml.** Грузится один раз на старте.
- **Нет per-route auth декларативного shorthand'а.** Используйте middleware-factory, зарегистрированные через `auth/fibermount.MountMiddlewareFactories`.
- **YAML-ошибки на parse-time, не edit-time.** Используйте `routes.schema.json` (см. [`schemas/`](../schemas/)) в вашем редакторе для live-валидации.
- **`Mount`/`Run` можно вызвать только один раз на engine.** Re-mount — это программерская ошибка (panic'ит).

## См. также

- [`fibermap/bind`](bind/README.md) — декодирование request body/query/header/params + валидация
- [`fibermap/factory`](factory/README.md) — хелперы для сборки middleware-factory
- [`fibermap/fibermaptest`](fibermaptest/README.md) — testing-хелперы для inventory Routes()
- [`fibermap/openapi`](openapi/README.md) — генерация OpenAPI 3.0 spec'а из `Engine.Routes()`
- [`schemas/`](../schemas/) — JSON-schema'ы для всех YAML-конфигов кита (routes/clients/crons/natsmap)
- [`auth/fibermount`](../auth/fibermount/README.md) — монтирует `bearer`/`require_scope`/`require_role` factory на engine
- [`errs`](../errs/README.md) — typed-error контракт, используемый `ErrorHandler`'ом
- [`examples/urlshort/`](../examples/urlshort/README.md) — полный интеграционный пример
</content>
