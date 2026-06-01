# service

All-in-one service-хелпер. Один `service.New(ctx, cfg)` собирает bundled runtime — `*db.DB`, `*auth.Auth[C]`, `*natsclient.Client`, `*natsmap.Runtime`, `*http.Client`, `*apimap.Client`, `*fibermap.Engine[T]` — с auto-detect optionality (подсистемы с пустым config'ом остаются nil). Авто-устанавливает `auth.Bearer(BearerOptional)` на уровне fiber.App через `WithUse`, так что `ContextBuilder` правильно читает JWT subject (фиксит реальную gotcha) и подключает factory-middleware `bearer:` на engine; `/auth/login` `/auth/refresh` `/auth/logout` НЕ авто-монтируются — объявите свой login-handler и зовите `svc.Auth.IssueLogin / IssueRefresh / Logout`. `Run()` блокирует production-ops бандлом. Service additive над существующими подпакетами — обращайтесь напрямую к `svc.DB.Tx(...)` / `svc.Auth.Sign(...)` для всего, что Service не shortcut'ит.

**Импорт:** `github.com/theizzatbek/gokit/service`
**Зависит от:** каждого другого `gokit/*` подпакета

## Зачем это нужно

Hand-rolled проводка kit-based сервиса — это ~200 строк: `KeySet` из PEM, plumbing `auth.New` + `refreshpg.New`, `httpc.New`, `apimap.New + LoadFile + Build` (с `${MICROLINK_BASE_URL}` env-трюком), `natsclient.Connect`, `fibermap.Default + SetValidator`, `fibermount.MountMiddlewareFactories`, установка `Bearer(BearerOptional)` на уровне fiber.App через `WithUse` (или молча попасться в ловушку "AppCtx.UserID пустой в хендлерах"), сборка `RunOption`'ов, управление graceful shutdown, настройка `slog`. `service` — это такой бандл. Ваш сервис всё ещё регистрирует свои auth-хендлеры (login body shape, проверка credential, кастомные auth-схемы) — обычно несколько строк, делегирующих в `svc.Auth.IssueLogin` / `IssueRefresh` / `Logout`.

`main.go` примера `examples/urlshort` сжимается с ~270 → ~80 строк после переключения на Service.

## Quickstart

```go
package main

import (
    "context"
    "log"
    "os"
    "os/signal"
    "syscall"

    "github.com/caarlos0/env/v11"
    "github.com/gofiber/fiber/v2"

    "github.com/theizzatbek/gokit/auth"
    "github.com/theizzatbek/gokit/fibermap"
    "github.com/theizzatbek/gokit/service"
)

type AppCtx struct{ UserID string }
type Claims struct {
    Email string `json:"email"`
}

func main() {
    var cfg service.Config
    if err := env.Parse(&cfg); err != nil { log.Fatal(err) }

    ctx, _ := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

    svc, err := service.New[AppCtx, Claims](ctx, cfg)
    if err != nil { log.Fatal(err) }
    defer svc.Close()

    svc.SetContextBuilder(func(c *fiber.Ctx) (AppCtx, error) {
        return AppCtx{UserID: svc.Auth.Subject(c)}, nil
    })

    // Кастомный login-handler — service владеет body-shape'ом и верификацией.
    type LoginRequest struct {
        Login    string `json:"login"    validate:"required"`
        Password string `json:"password" validate:"required,min=1"`
    }
    fibermap.RegisterHandlerWithBody(svc.Engine, "auth.login",
        func(c *fibermap.Context[AppCtx], body LoginRequest) error {
            // look up user, check password ...
            return svc.Auth.IssueLogin(c.Ctx, auth.LoginResult[Claims]{
                Subject: "uid",
                Custom:  Claims{Email: body.Login},
            })
        })
    fibermap.RegisterHandler(svc.Engine, "auth.refresh",
        func(c *fibermap.Context[AppCtx]) error { return svc.Auth.IssueRefresh(c.Ctx) })
    fibermap.RegisterHandler(svc.Engine, "auth.logout",
        func(c *fibermap.Context[AppCtx]) error { return svc.Auth.Logout(c.Ctx) })

    fibermap.RegisterHandler(svc.Engine, "ping", func(c *fibermap.Context[AppCtx]) error {
        return c.SendString("pong")
    })

    if err := svc.Run(); err != nil { log.Fatal(err) }
}
```

## Конфигурация

Env-driven через `caarlos0/env/v11`. Композируйте в собственный app-config через embedding, чтобы добавить app-specific поля.

### K8s boot-resilience

Service инициализирует DB и NATS с bounded retry (5 попыток,
1s→16s экспоненциальный backoff) по умолчанию — учитывает обычный
паттерн, где postgres/nats контейнеры Ready на несколько секунд после
старта service-контейнера. Opt out через `WithoutConnectRetry()`
или установите per-subsystem env-sentinel `_CONNECT_MAX_RETRIES=-1`.

### Top-level `service.Config`

| Раздел | Префикс | Триггер | Заметки |
|---|---|---|---|
| `Service` | (нет) | всегда | `ADDR`, `LOG_LEVEL`, `LOG_FORMAT` |
| `DB` | `DB_` | `DB_USER` установлен | Когда пропущен, `svc.DB == nil` |
| `Auth` | `AUTH_` | `AUTH_PRIVATE_KEY_PEM` установлен | Требует DB (refreshpg store) |
| `NATS` | `NATS_` | `NATS_URL` установлен | Независимо |
| `NATSMap` | `NATSMAP_` | `NATSMAP_ENABLED=true` или path установлен | Требует NATS |
| `HTTPC` | `HTTPC_` | всегда | Zero-value → разумные defaults |
| `APIMap` | `APIMAP_` | `APIMAP_ENABLED=true` или `APIMAP_PATH` установлен | Clients YAML |
| `Routes` | `ROUTES_` | `ROUTES_ENABLED=true` или `ROUTES_PATH` установлен | Routes YAML |

### `ServiceConfig`

| Поле | Env | По умолчанию |
|---|---|---|
| `Addr` | `ADDR` | `:3000` |
| `LogLevel` | `LOG_LEVEL` | `info` |
| `LogFormat` | `LOG_FORMAT` | `json` (также: `text`) |
| `NodeName` | `SERVICE_NODE_NAME` | `os.Hostname()` если не установлен. Идёт в `natsclient.Config.Name` (когда `NATS.Name` не explicit) и в default slog-attr'ы (`node=...`). |
| `ServerGroup` | `SERVICE_SERVER_GROUP` | Пусто по умолчанию. Когда установлен, передаётся в `natsmap.WithServerGroup(...)` — авто-выведенные subscriber queue-groups суффиксят `-<ServerGroup>` для cross-region изоляции. См. [natsmap multi-node](../clients/natsmap/README.md#multi-node-behaviour). |
| `ConfigsDir` | `CONFIGS_DIR` | Пусто = текущий CWD-based lookup (`routes.yaml`, `clients.yaml`, …). Когда установлен (например, `configs`), каждый default-named YAML резолвится в `<ConfigsDir>/<name>.yaml`. Per-subsystem `Path`-override'ы (`ROUTES_PATH`, `APIMAP_PATH`, `NATSMAP_*_PATH`) обходят prefix — operator-typed пути уважаются literal. См. [Default paths convention](#конвенция-default-путей). |

### `DBConfig`

Полный список полей живёт в [db/README](../db/README.md#конфигурация). Multi-node-relevant env vars, выставленные через service:

| Поле | Env | Заметки |
|---|---|---|
| `URL` | `DB_URL` | полная postgres connection-строка (override'ит `DB_HOST`/`DB_PORT`/…). Поддерживает comma-separated multi-host URL для primary-failover. |
| `AppName` | `DB_APP_NAME` | `application_name`, отправляемое в Postgres; авто-установлено из `SERVICE_NODE_NAME` при пустом. |
| `HasReadReplica` | `DB_HAS_READ_REPLICA` | opt в standby-пул; `svc.DB.ReadQuery(...)` тогда таргетит standby. Требует PG 14+. |

### `AuthConfig`

| Поле | Env | По умолчанию |
|---|---|---|
| `PrivateKeyPEM` | `AUTH_PRIVATE_KEY_PEM` | (opt-in trigger) |
| `KID` | `AUTH_KID` | `k1` |
| `Issuer` | `AUTH_ISSUER` | `gokit` |
| `AccessTTL` | `AUTH_ACCESS_TTL` | `15m` |
| `RefreshTTL` | `AUTH_REFRESH_TTL` | `720h` (30 дней) |

### `NATSConfig`

| Поле | Env |
|---|---|
| `URL` | `NATS_URL` |
| `Name` | `NATS_NAME` |

### `RedisConfig`

| Поле | Env |
|---|---|
| `URL` | `REDIS_URL` |
| `ConnectMaxRetries` | `REDIS_CONNECT_MAX_RETRIES` |
| `ConnectBackoffBase` | `REDIS_CONNECT_BACKOFF_BASE` |
| `ConnectBackoffMax` | `REDIS_CONNECT_BACKOFF_MAX` |

`URL` — opt-in trigger. Когда установлен, `service.New` зовёт
`redisclient.Connect` (со стандартным retry-budget'ом), exposes
результат как `svc.Redis` и tears down его в `Close`. Layer'ните
типизированный cache сверху через [`clients/cache`](../clients/cache/README.md).

### `APIMapConfig`

| Поле | Env |
|---|---|
| `Enabled` | `APIMAP_ENABLED` |
| `Path` | `APIMAP_PATH` |

### `NATSMapConfig`

| Поле | Env |
|---|---|
| `Enabled` | `NATSMAP_ENABLED` |
| `SubscribersPath` | `NATSMAP_SUBSCRIBERS_PATH` |
| `PublishersPath` | `NATSMAP_PUBLISHERS_PATH` |

Любой path (или `Enabled=true`) триггерит auto-build через `clients/natsmap`. Оба path могут указывать на тот же combined-YAML. Требует `NATS` сконфигурированным (`service_natsmap_needs_nats` иначе).

### `RoutesConfig`

| Поле | Env |
|---|---|
| `Enabled` | `ROUTES_ENABLED` |
| `Path` | `ROUTES_PATH` |

Когда `Enabled=true` или `Path` установлен, routes YAML загружается и монтируется на `svc.Run()`-time. Если `Path` пуст и `Enabled=true`, используется `service.DefaultRoutesPath` (`routes.yaml`).

## Конвенция default путей

Каждая YAML-driven подсистема выставляет `Enabled`-флаг плюс опциональный `Path`-override. Когда `Enabled=true` и нет установленного `Path`, service использует канонический default filename — кидаете файл в working-directory вашего бинаря, и всё.

**Folder layout через `CONFIGS_DIR`.** Установите `ServiceConfig.ConfigsDir` (env `CONFIGS_DIR`), чтобы держать все четыре YAML'а под одной папкой:

```
my-service/
├── main.go
├── go.mod
└── configs/
    ├── routes.yaml
    ├── clients.yaml
    ├── subscribers.yaml
    └── publishers.yaml
```

С `CONFIGS_DIR=configs` каждый default-named lookup резолвится в `configs/<name>.yaml`. Per-subsystem `Path`-override'ы обходят prefix — `ROUTES_PATH=/etc/foo.yaml` остаётся `/etc/foo.yaml`, так что операторы, тюнящие один файл через env, всё ещё получают literal-путь, который ввели.

| Подсистема | Enabled env | Default filename | Path override env |
|---|---|---|---|
| apimap | `APIMAP_ENABLED` | `service.DefaultAPIMapPath` (`clients.yaml`) | `APIMAP_PATH` |
| natsmap subscribers | `NATSMAP_ENABLED` | `service.DefaultNATSMapSubscribersPath` (`subscribers.yaml`) | `NATSMAP_SUBSCRIBERS_PATH` |
| natsmap publishers | `NATSMAP_ENABLED` | `service.DefaultNATSMapPublishersPath` (`publishers.yaml`) | `NATSMAP_PUBLISHERS_PATH` |
| routes | `ROUTES_ENABLED` | `service.DefaultRoutesPath` (`routes.yaml`) | `ROUTES_PATH` |

**Trigger-логика** (та же для каждой подсистемы):
- Построить подсистему, если `Enabled=true` **ИЛИ** matching `Path`-поле установлено.
- Если `Path` пуст и `Enabled=true`, использовать default-const.
- Override `Path` всегда побеждает.

**Missing-файлы:**
- Explicit `Path`-override'ы strict — missing-файл производит `service_*_yaml_not_found`.
- Default-пути (через `Enabled=true`) strict для apimap и routes (единственный файл).
- NATSMap default-пути silent-skip на miss — поддерживает publish-only и subscribe-only сервисы, которые drop'ают только один из двух файлов. Если оба default-файла отсутствуют, возвращает `service_natsmap_yaml_not_found`.

## OpenAPI из routes.yaml

Объявите OpenAPI-метаданные документа рядом с вашими роутами:

```yaml
groups:
  - prefix: /v1
    routes: [...]

openapi:
  info:
    title: My API
    version: 1.0.0
    description: Public REST API.
    contact:
      name: Maintainer
      email: maintainer@example.com
  servers:
    - url: https://api.example.com
      description: production
  security_schemes:
    BearerAuth:
      type: http
      scheme: bearer
      bearer_format: JWT
  middleware_security:
    auth: [BearerAuth]
```

Когда этот блок присутствует, `service` монтирует `/openapi.json` и `/docs`
автоматически. Зовите `service.WithOpenAPI()`, чтобы opt in explicitly без
YAML-блока (использует openapi-package defaults), или передавайте openapi-опции,
чтобы override'нуть или augment YAML-значения:

```go
service.WithOpenAPI(
    openapi.WithInfo(openapi.Info{Title: "Override", Version: "2"}),
    openapi.WithDefaultResponse(404, ErrorResp{}),
)
```

**Приоритет:** YAML применяется первым. Потом user-opts. `Info`:
last-write-wins (код override'ит). `Servers` / `SecuritySchemes` /
`MiddlewareSecurity`: accumulating append.

**Вне scope'а для YAML:** `WithDefaultResponse(status, model)` и
типизированные schema-builder'ы (`gen.OnHandler(...).Body(...).Response(...)`)
нуждаются в Go-типах — передавайте их через option-chain.

### Code-driven vs env-driven enable

Два эквивалентных способа opt in'нуть:

- **Code:** передайте `service.WithAPIMap()` / `WithNATSMap()` / `WithRoutes()` в `service.New`. Лучше всего, когда `main.go` уже цепляет другие `With*`-опции.
- **Env:** установите `APIMAP_ENABLED=true` / `NATSMAP_ENABLED=true` / `ROUTES_ENABLED=true`. Лучше всего для env-driven деплоев, где Go-side флаги были бы неудобны.

Оба flip'ают тот же internal-флаг; передавайте любой или оба — оба, устанавливающие `Enabled = true`, идемпотентны.

## Опции

| Опция | Заметки |
|---|---|
| `WithOpenAPI(opts ...openapi.Option)` | Включает OpenAPI mounting. Без args Info/Servers/SecuritySchemes/MiddlewareSecurity приходят из top-level блока `openapi:` в `routes.yaml`. Передавайте `openapi.WithInfo(...)` / `WithServer(...)` / `WithSecurity(...)` / `WithDefaultResponse(...)`, чтобы override'нуть или augment. Авто-монтируется даже без этого вызова, когда YAML-блок присутствует. |
| `WithLogger(*slog.Logger)` | Override авто-built логгера |
| `WithMetrics(prometheus.Registerer)` | Override дефолтного `prometheus.NewRegistry()` |
| `WithoutRuntimeMetrics()` | Пропустить авто-регистрацию `go_*` runtime + `process_*` коллекторов на service-registry. Используйте, когда caller уже их зарегистрировал, или чтобы держать scrape-вывод kit-only. |
| `WithValidator(bind.Validator)` | Override default `validator.New(validator.WithRequiredStructEnabled())`. Используйте, чтобы зарегистрировать кастомные валидаторы (`v.RegisterValidation("safe_url", …)`) или поменять реализации целиком. |
| `WithFiberMiddleware(handlers...)` | Вставить fiber-level middleware перед engine (helmet, otelfiber, …) |
| `WithCORS(origins...)` | Shortcut для `fiber/v2/middleware/cors` с kit-defaults: REST-методы, common-headers, `X-Request-ID` exposed, MaxAge 24h. Credentials on для explicit-origins; auto-off, когда `"*"` listed (CORS-спек). |
| `WithCORSConfig(cors.Config)` | Full-control CORS — `cfg` передаётся прямиком в `cors.New`. |
| `WithoutBearerOptionalLayer()` | Пропустить авто `Bearer(BearerOptional)`-установку |
| `WithRefreshGC(interval)` | Schedule periodic `RefreshStore.GarbageCollect` против auth refresh-store, так что expired-токены prune'ятся. INFO-лог per non-zero sweep; WARN на failure. Bound к `OnShutdown` для clean-stop. Interval ≤ 0 = disabled. No-op, когда Auth не сконфигурирован. |
| `WithOtel(serviceName, otelkit.Option...)` | Включает OpenTelemetry tracing И metrics. Tracing: инициализирует TracerProvider через OTLP/HTTP (`otelkit.Setup`), prepend'ит `otelfiber`-middleware (inbound-span'ы), оборачивает httpc base-transport в `otelhttp` (outbound-span'ы + W3C propagation). Metrics: мостит service-registry на OTLP/HTTP через `otelkit.SetupMetrics`, когда registry — это `prometheus.Gatherer`. Оба регистрируют shutdown через `OnShutdown`. Конфигурируйте exporter через стандартные env vars `OTEL_EXPORTER_OTLP_*`. См. [otelkit](../otelkit/README.md). |
| `WithSentry(dsn, sentrykit.Option...)` | Включает Sentry error-tracking. Зовёт `sentrykit.Setup` (валидирует DSN, применяет environment/release/tags/sample-rate/BeforeSend хуки), append'ит `sentrykit.FiberMiddleware` (per-request hub clone + panic auto-capture, который re-panic'ит, так что `fibermap.Recover` всё ещё пишет 500), регистрирует shutdown через `OnShutdown` (flush'ит ДО OTel во время Close, так что события держат свой trace_id). Auto-wrap'ит kit-built логгер с `sentrykit.SlogHandler`, так что каждая subsystem log-запись становится breadcrumb'ом на request-hub'е (user-supplied логгеры через `WithLogger` НЕ оборачиваются — передавайте `sentrykit.SlogHandler(yourHandler)` сами). 5xx auto-capture opt-in — оборачивайте свой error-handler с `sentrykit.WrapErrorHandler`. См. [sentrykit](../sentrykit/README.md). |
| `WithSentryBreadcrumbs(sentrykit.HandlerOption...)` | Конфигурирует slog→breadcrumb-мост, авто-установленный `WithSentry`. Форвардит опции в `sentrykit.SlogHandler`: `WithDebugBreadcrumbs`, `WithAttrFilter`, `WithCategoryAttr`, `WithMaxBreadcrumbValueLen`, `WithCaptureDedupeWindow`, `WithCaptureErrorAttrKeys`. No-op без `WithSentry`. |
| `WithSentryErrorCapture(slog.Level)` | Включает Sentry event auto-capture для log-записей ≥ level (типично `slog.LevelError`). Записи, несущие `err`/`error`/`cause`-attr типа `error`, ship'ятся как Sentry-Exception'ы (stack-frames); иначе как Message-события. Dedup'ит по `(level, category, message)` внутри 60s по умолчанию — override через `WithSentryBreadcrumbs(sentrykit.WithCaptureDedupeWindow(d))`. No-op без `WithSentry`. |
| `WithoutSentryUserScope()` | Пропустить auto-tagging Sentry-событий с `sentry.User{ID: principal.Subject}`. Default-поведение: когда и `WithSentry`, и Auth сконфигурированы, каждое событие, captured во время аутентифицированного запроса, несёт JWT subject как "Affected User" в Sentry. Отключите, когда Subject PII в вашем деплое (например, это email пользователя) — handlers всё ещё могут установить User-scope руками с хешированными/redacted значениями через `sentrykit.HubFromContext(c).Scope().SetUser(...)`. |
| `WithSentryRefreshGCSlug(slug)` | Override default `"kit-refresh-gc"` Sentry monitor-slug, используемого refresh-token GC-ticker'ом. Полезно, когда несколько kit-based сервисов шарят один Sentry-проект (например, `"orders-refresh-gc"`). Без эффекта, если `WithSentry` и `WithRefreshGC` оба не установлены. |
| `WithoutSentryRefreshGCMonitor()` | Отключает Sentry Crons check-in'ы для refresh-GC ticker'а (tracing / breadcrumbs / error-capture остаются on). Используйте в multi-replica деплоях, где каждая реплика tick'а эмитила бы тот же slug — Sentry не deduplicate'ит, так что один сконфигурированный monitor получал бы один heartbeat per replica per tick. |
| `WithOtelMetricsOptions(otelkit.MetricsOption...)` | Конфигурирует OTel metrics-пайплайн, авто-включённый `WithOtel`'ом (interval, exporter-опции, resource-атрибуты). No-op без `WithOtel`. |
| `WithoutOtelMetrics()` | Подавить Prometheus→OTel metrics-мост, который `WithOtel` иначе auto-включает. Tracing всё ещё запускается. Используйте, когда деплой уже скрейпит `/metrics` напрямую и не хочет параллельной push-пайплайн. |
| `WithoutConnectRetry()` | Отключает auto-injected K8s-friendly retry-defaults для DB и NATS Connect. Без этого, service defaults к 5 retries с 1s→16s экспоненциальным backoff'ом (~31s budget). См. db/README и clients/nats/README. |
| `WithHTTPCOptions(opts...)` | Extra httpc-опции (logger + metrics уже авто-применены) |
| `WithAPIMapOptions(opts...)` | Extra apimap-опции |
| `WithAPIMap()` | Эквивалентно `Config.APIMap.Enabled = true`. Apimap auto-build'ится из `service.DefaultAPIMapPath` (`clients.yaml`), когда `Path`-override не установлен. Missing-файл → `service_apimap_yaml_not_found`. |
| `WithAPIMapRegistration(fn)` | Зарегистрировать типизированные Request/Response модели ДО того, как `apimap.Build` запечатывает engine |
| `WithAPIMapEnv(m map[string]string)` | Explicit `${VAR}`-значения для apimap'ового clients.yaml. Map consult'ируется перед `os.LookupEnv`. |
| `WithNATSMap()` | Эквивалентно `Config.NATSMap.Enabled = true`. Natsmap auto-build'ится из дефолтных subscribers/publishers путей. Требует NATS. |
| `WithNATSMapRegistration(fn)` | Зарегистрировать типизированные subscriber-handler'ы + publishers через `natsmap.RegisterHandler[T]` / `natsmap.RegisterPublisher[T]` ДО того, как `natsmap.Build` открывает subscriptions. Обязательно, когда `NATSMap.*Path` установлен. |
| `WithNATSMapEnv(m map[string]string)` | Explicit `${VAR}`-значения для natsmap'овых subscribers/publishers YAML. Map consult'ируется перед `os.LookupEnv`. |
| `WithRoutes()` | Эквивалентно `Config.Routes.Enabled = true`. Routes auto-load'ятся в `svc.Run()` из `service.DefaultRoutesPath` (`routes.yaml`). |
| `WithNATSOptions(opts...)` | Extra natsclient-опции |
| `WithRedisOptions(opts...)` | Extra redisclient-опции (logger + metrics авто-применены); используйте `redisclient.WithRedisOptions(fn)`, чтобы установить поля `redis.Options` вроде `PoolSize` или `TLSConfig`. |
| `WithRunOptions(opts...)` | Append `fibermap.RunOption`'ов к дефолтному production-ops бандлу |
| `WithoutReadiness()` | Подавить авто-смонтированный `/readyz` probe. Liveness (`/healthz`) остаётся on. |
| `WithReadinessPath(path)` | Override дефолтной `/readyz` mount-точки. |
| `WithReadinessTimeout(d)` | Per-probe deadline для full checker-set; форвардится в `fibermap.WithReadinessOpts`. 0 → встроенный дефолт fibermap'а (5s). |
| `WithReadinessChecker(c...)` | Append app-level checkers (migrate-gate, cache-warmup, external API-ping) к авто-проводимому DB / NATS / Redis сету. |
| `WithoutSecurityHeaders()` | Подавить авто-установленные OWASP security-headers (HSTS, X-Content-Type-Options, X-Frame-Options, Referrer-Policy, CSP). Используйте, когда headers обрабатываются upstream (CDN, reverse-proxy). |
| `WithSecurityHeaders(fibermap.SecurityHeadersOption...)` | Кастомизировать авто-установленные security-headers — форвардит `fibermap.WithHSTSIncludeSubdomains`, `WithCSP`, `WithoutHSTS` и т.д. Middleware всё равно устанавливается; передайте `WithoutSecurityHeaders`, чтобы подавить. |
| `WithBodyLimit(bytes)` | Cap inbound request-body (Fiber возвращает 413 над limit'ом). 0 → Fiber-default (4 MiB). Теряет caller-supplied `fibermap.WithFiberConfig` через `WithRunOptions`. |
| `WithDBOptions(opts...)` | Extra `db.Option`'ы, применяемые к kit-built `*db.DB`. Logger уже подключён; reach for this, чтобы добавить `db.WithMetrics`, `db.WithSlowQueryThreshold`, дополнительные `db.WithTracer` (audit / кастомные backends) и т.д. |
| `WithOtelPgxOptions(opts...)` | Конфигурирует OTel pgx-tracer, авто-attach'енный `WithOtel`'ом. Форвардит `otelkit.WithPgxTracerName`, `WithPgxSpanNamer`, `WithoutPgxSQL`, `WithPgxMaxSQLLength`. No-op без `WithOtel`. |
| `WithoutOtelPgxTracer()` | Подавить авто-проводимый OTel pgx-tracer. HTTP-path tracing (otelfiber / otelhttp) остаётся on. Используйте, когда DB tracing предоставляется sidecar'ом, или когда per-query span-volume взорвал бы export-budget. |
| `WithMigrations(fsys fs.FS)` | Применить `embed.FS`-миграции через [`db/migrate.Up`](../db/migrate/README.md) после buildDB и до любой подсистемы, читающей schema (auth.refreshpg, outbox, apikeypg). |
| `WithCron(name, schedule, fn)` | Зарегистрировать recurring-job на config-time. 5-field cron-формат (override через `WithCronParser`). Auto-wrap'ит с `sentrykit.MonitorCron`, когда `WithSentry` подключён. |
| `WithCronSlug(jobName, slug)` | Override авто-derived Sentry Crons monitor-slug. |
| `WithCronParser(parser)` | Кастомный cron-parser (например, 6-field с секундами для sub-minute job'ов). |
| `WithoutLoggerInjector()` | Пропустить авто-установленный [`fibermap.LoggerInjector`](../fibermap/README.md#request-scoped-logger) middleware. |
| `WithSingletonCron(name, schedule, fn)` | Как `WithCron`, но оборачивает `fn` в `pg_try_advisory_lock(hash(name))`, так что только ОДНА реплика per multi-replica деплой запускает job per tick. Требует DB. |
| `svc.AddSingletonCron(name, schedule, fn)` | Post-build counterpart для job'ов, чьё closure нуждается в `svc.DB`. Та же lock-семантика. |
| `WithOutboxReadinessOpts(outbox.CheckerOption...)` | Тюнить авто-установленный outbox backlog-check на `/readyz`. Defaults `WithMaxDepth(10000)`, `WithMaxLag(10*time.Minute)`. |
| `WithoutOutboxReadiness()` | Отключить авто-установленный outbox readiness-check. |
| `WithOutbox(outbox.WorkerOption...)` | Включить transactional outbox-worker. Требует DB + (NATSMap ИЛИ `WithOutboxDispatcher`). Auto-wires logger + metrics, регистрирует `OnShutdown(Stop)`. Default PublishFn = `natsmap.PublishRaw(ctx, rt, e.EventType, e.Payload, e.Headers)`. |
| `WithOutboxDispatcher(fn)` | Override default outbox PublishFn (например, fan out в несколько subjects, оборачивать audit-логом, диспатчить на не-natsmap шину). |
| `WithOutboxAutoSchema()` | Применить `outbox.Schema()` на boot'е. Off по умолчанию — большинство деплоев впихивают DDL в свой migration-tool. |
| `WithoutOtelLogs()` | Подавить slog→OTel logs-мост, который `WithOtel` иначе auto-включает. Tracing и metrics остаются on. Используйте, когда логи отправляются sidecar-shipper'ом (Promtail, Vector). |
| `WithOtelLogsOptions(otelkit.LogsOption...)` | Конфигурирует OTel logs-пайплайн, авто-включённый `WithOtel`'ом (resource-атрибуты, exporter-override'ы). No-op без `WithOtel`. |
| `WithDBDrainTimeout(d)` | Cap на ожидание in-flight DB-запросов / транзакций во время `Service.Close`. По умолчанию 5s. `svc.DB.Drain(ctx)` вызывается с этим deadline'ом перед hard-Close. |
| `WithS3Options(s3client.Option...)` | Extra `s3client.Option` для kit-built `*s3client.Client`. Logger + Metrics уже auto-wired'ы; reach for this для custom-retry policy через AWS SDK config. |
| `WithRateLimit(ratelimit.Config, opts...)` | Opt-in kit-овского Redis-backed sliding-window лимитера (`svc.RateLimiter`). Регистрирует YAML middleware factory `rate_limit_redis` на Engine. Требует Config.Redis.URL (иначе `service_ratelimit_needs_redis`). Когда Auth тоже сконфигурирован — `user`/`subject` key-strategy резолвится через `auth.KeyBySubject[C]` автоматически. |
| `WithPreflightEndpoint(path)` | Mounts `/preflight` (path override-able) returning JSON `{status, checks[]}`. 200 на success, 503 на любой failure. Используется `kit doctor` CLI'ём для CI-gating'а + on-call-smoke-checks. Сами checks — те же что run'ятся `/readyz` plus opt-in custom-чекеры через `WithReadinessChecker`. |
| `WithPreflightTimeout(d)` | Cap на time-budget'е всего preflight-run'а. Default 10s — accommodates slow one-shot validations (S3 HEAD, schema-version SELECT). |
| `WithDevMode(prefix, dev.ConfigOption...)` | Auto-mount dev-tools: HTML-error-pages + `/_dev/routes` route-inspector + `/_dev/config` env-inspector (с redaction). **No-op когда `Config.Service.Env != "dev"`** + warn-log. См. [`fibermap/dev`](../fibermap/dev/README.md). |

## Common patterns

### Композирование собственного app-config'а

```go
type Config struct {
    service.Config
    ShortURLBase string `env:"SHORT_URL_BASE" envDefault:"http://localhost:3000"`
}

var cfg Config
_ = env.Parse(&cfg)
svc, _ := service.New[AppCtx, Claims](ctx, cfg.Config)
```

### Регистрация типизированных apimap response-моделей

```go
svc, _ := service.New[AppCtx, Claims](ctx, cfg,
    service.WithAPIMapRegistration(func(e *apimap.Engine) {
        apimap.RegisterResponse[User](e, "github.get_user")
        apimap.RegisterRequest[NewIssue](e, "github.create_issue")
        apimap.RegisterResponse[Issue](e, "github.create_issue")
    }),
)
```

Без этого `apimap.Decode[User]` возвращает generic JSON. После того, как `Build` запускается (внутри `service.New`), engine sealed — регистрации должны происходить через эту опцию.

### Инъекция otelhttp / helmet

```go
svc, _ := service.New[AppCtx, Claims](ctx, cfg,
    service.WithFiberMiddleware(
        helmet.New(),
        cors.New(cors.Config{AllowOrigins: "*"}),
    ),
)
```

Fiber-level middleware запускается ДО engine'овского contextInit, рядом с авто-установленным `Bearer(BearerOptional)`-слоем.

### Кастомный cleanup через `OnShutdown`

`svc.Close()` tears down только то, что Service построил. Для app-specific ресурсов (workers, third-party clients, Sentry / metrics pushers, scheduled jobs) зарегистрируйте callback:

```go
svc, _ := service.New[AppCtx, Claims](ctx, cfg)
defer svc.Close()

worker := startWorker(svc.DB)
svc.OnShutdown(worker.Stop)

scheduler := startScheduler()
svc.OnShutdown(scheduler.Shutdown)
```

Callback'и запускаются на `Close()`:
1. **Сначала**, зарегистрированные callback'и срабатывают в LIFO-порядке. Kit-подсистемы (DB, NATS, …) всё ещё живы, так что callback'и могут flush'ить in-flight state.
2. Потом `NATSMap.Drain()` (in-flight handlers заканчивают).
3. Потом `NATS.Close()`.
4. Наконец `DB.Close()`.

Ошибки, возвращённые callback'ом, логируются через `svc.Logger()` и НЕ abort'ят последующие callback'и или subsystem-teardown. `OnShutdown` thread-safe; вызов его после `Close` — это no-op.

### Обход Service для одной операции

Service выставляет все deps как публичные поля — drop down всегда, когда нужен fine-control:

```go
err := svc.DB.Tx(ctx, func(tx *db.Tx) error {
    // multi-statement транзакция
    return nil
})

pub := natsclient.NewPublisher[MyEvent](svc.NATS)
pub.Publish(ctx, "my.event", MyEvent{...})

resp, _ := svc.HTTPC.Get("https://example.com")
```

## Error-модель

`service.New` возвращает `*errs.Error` с `Kind`/`Code`:

| Code | Kind | Когда |
|---|---|---|
| `service_auth_needs_db` | Validation | Auth сконфигурирован, но DB нет |
| `service_auth_invalid_key` | Validation | PEM unparseable или wrong-алгоритм |
| `service_db_connect_failed` | Unavailable | `db.Connect` зафейлился (wrapped) |
| `service_apimap_load_failed` | Validation | apimap LoadFile / Build зафейлились (wrapped) |
| `service_nats_connect_failed` | Unavailable | `natsclient.Connect` зафейлился (wrapped) |
| `service_natsmap_needs_nats` | Validation | NATSMap сконфигурирован, но NATS нет |
| `service_natsmap_load_failed` | Validation | natsmap LoadFile / Build зафейлились (wrapped) |
| `service_httpc_new_failed` | Validation | `httpc.New` валидация зафейлилась (wrapped) |
| `service_openapi_mount_failed` | Internal | OpenAPI Mount зафейлился |

Subsystem-specific ошибки пропагируют как `Cause` — используйте `errors.As`, чтобы extract'ить.

## Observability

- `svc.Logger()` возвращает `*slog.Logger`, который получила каждая подсистема.
- `svc.Metrics()` возвращает `prometheus.Registerer`, в который каждая подсистема регистрируется.
- Все `WithLogger`/`WithMetrics`-опции подсистем авто-применяются; вы не передаёте их per call.
- **Унифицированный `/metrics`-scrape.** `svc.Run()` routes `/metrics`-эндпоинт через тот же registry, так что один scrape экспонирует `fibermap_http_*` (router), `db_*`, `httpc_*`, `nats_*`, `natsmap_*` вместе. `go_*` (heap, GC, goroutines) и `process_*` (FDs, RSS, CPU-секунды) runtime-коллекторы авто-зарегистрированы на тот же registry — отключите через `service.WithoutRuntimeMetrics()`.
- **apimap-метрики** shipping'ятся под собственным `apimap_*`-namespace'ом (`apimap_requests_total`, `apimap_request_duration_seconds`) с label'ами `client`+`endpoint`+`status`, так что per-upstream visibility приземляется на shared-registry без коллизий с kit'овыми `httpc_*`-коллекторами. apimap больше не форвардит registry в свои внутренние httpc-клиенты; если нужны и apimap-level, и per-attempt httpc-вьюхи, постройте dedicated httpc с отдельным registry вне apimap.

## Порядок shutdown

`svc.Close()` дренит `NATSMap` (так что in-flight subscriber-handler'ы заканчивают) **до** того, как tears down `NATS`-соединение. Downstream-подсистемы (`DB`, `Auth`) закрываются последними. Всегда `defer svc.Close()` после `service.New`.

## Тестирование

Для unit-тестов empty-config-путь строит Service только с Engine + HTTPC:

```go
svc, _ := service.New[AppCtx, Claims](ctx, service.Config{})
```

Для integration-тестов отзеркальте `examples/urlshort/main_test.go` — используйте testcontainers (Postgres + NATS), постройте Service с full-config'ом, mount'ните Engine на `*fiber.App`, драйвите через `app.Test`.

## Ограничения

- **Только `refreshpg`.** Никакого `refreshredis`-селектора — сервисы, которые хотят Redis, обходят Service для auth-лестницы.
- **Никаких миграций.** Применяйте свой SQL (`db.Exec(string(fileBytes))`) перед регистрацией хендлеров.
- **Нет background-job runner'а.** Out of kit scope.
- **`New` не concurrency-safe.** Конструируйте один раз на процесс.
- **Service не зеркалит каждый subpkg-метод.** Доступ к подсистемам через публичные поля: `svc.DB.Tx(...)`, `svc.Auth.Sign(...)` и т.д.
- **apimap-метрики off по умолчанию** (см. Observability выше).

## См. также

- [`fibermap`](../fibermap/README.md), [`errs`](../errs/README.md), [`db`](../db/README.md), [`auth`](../auth/README.md), [`clients/httpc`](../clients/httpc/README.md), [`clients/apimap`](../clients/apimap/README.md), [`clients/nats`](../clients/nats/README.md), [`clients/natsmap`](../clients/natsmap/README.md), [`fibermap/openapi`](../fibermap/openapi/README.md)
- [`examples/urlshort`](../examples/urlshort/README.md) — Service, используемый end-to-end
</content>
