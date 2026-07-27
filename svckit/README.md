# svckit

Модульное ядро сервиса — младший брат `service/`, для случаев, когда бинарь должен весить столько, сколько сервис реально использует, а не столько, сколько кит умеет собрать.

**Импорт:** `github.com/theizzatbek/gokit/svckit`
**Зависит от:** `fibermap`, `errs`, `db`, `auth`, `clients/httpc` — и больше ничего опционального. Тест `TestCoreDoesNotImportOptionalSubsystems` держит это инвариантом: попадание `clients/s3`, `clients/redis`, `clients/nats`, `clients/natsmap`, `clients/apimap`, `clients/ratelimit`, `clients/webhooks`, `cronmap`, `db/outbox`, `otelkit` или `sentrykit` в `go list -deps ./svckit` красит CI, а не молча регрессирует размер бинаря.

## Зачем это нужно

`service.New` статически вызывает конструктор каждой опциональной подсистемы (`buildS3`, `buildRedis`, `buildNATS`, `setupOtel`, …) и держит в `Service` поля их типов. Рантайм-проверка `if cfg.S3.Bucket == ""` внутри билдера линкеру не видна — код недостижим только по данным, а не структурно, поэтому линкер ничего не выбрасывает. Результат: `service.New[struct{}, struct{}]` с пустым `Config{}` весит **39.03 MB**, даже если сервис использует только `db` + `auth` + роутинг.

`svckit` убирает эту статическую связь. Ядро не импортирует ни один опциональный клиент; каждая подсистема живёт в своём **моде** под `svckit/mods/` и попадает в бинарь только тогда, когда `main` действительно на неё ссылается. Замеры (`-ldflags="-s -w"`, Go 1.26.5, один и тот же код):

| сборка | размер | против v1 |
|---|---|---|
| `service.New`, пустой Config | 39.03 MB | — |
| `svckit.New`, без единого мода | 20.38 MB | −18.65 MB / −47.8% |
| `svckit.New` + `s3mod` | 27.64 MB | −11.40 MB / −29.2% |

Экономия достаётся не покупкой новых возможностей, а отказом от того, что сервис не использует. Смонтируйте все восемь модов — и вес сойдётся обратно к уровню v1: `svckit` не может стоить меньше, чем реально сконфигурированные подсистемы, только не больше.

## Отношение к `service/`

`service/` не тронут и не будет тронут этим пакетом напрямую — следующим шагом (отдельный план) он станет фасадом над `svckit`, вызывающим `svckit.New` со всеми модами и раскладывающим их хендлы по своим публичным полям. Публичный API v1 при этом сохранится байт-в-байт. До тех пор оба пакета живут независимо и `service/` весит столько же, сколько всегда.

Если ваш сервис уже на `service/` и вас устраивает 39 MB — оставайтесь на нём, миграция не обязательна. Переходите на `svckit`, когда размер бинаря или состав модов (не всё сразу, а по потребности) действительно важны — типично контейнерные образы, cold-start в serverless, multi-tenant деплой с десятками бинарей.

## Контракт `Mod` / `Host`

Ядро не знает ни одного мода по имени — оно видит только два не-generic интерфейса.

```go
// Mod — минимум, который обязан знать о себе каждый мод.
type Mod interface {
    Name() string // стабильный id: "s3", "redis", "nats" — попадает в Status(), логи, тексты ошибок
}

// Опциональные фазы — ядро определяет их через type assertion.
type Setuper interface { Setup(ctx context.Context, h Host) error } // до логгера и HTTPC
type Builder interface { Build(ctx context.Context, h Host) error } // сетевые клиенты поверх DB
type Wirer   interface { Wire(h Host) error }                       // после того как Engine собран

// Statuser и Enabler — опциональные уточнения для Status().
type Statuser interface { Status() any }
type Enabler  interface { Enabled() bool }
```

`Host` — то, что ядро отдаёт моду вместо целого `*Service[T, C]`. Он намеренно без типовых параметров: мод не должен знать ни `T` (payload движка), ни `C` (claims). Там, где generic всё же нужен — YAML middleware factory и rate-limit ключ по subject'у — ядро отдаёт не-generic срез (`FactoryFunc`, `SubjectKeyFn`), а не заставляет мод параметризоваться.

```go
type Host interface {
    Logger() *slog.Logger
    SetLogger(*slog.Logger)              // otelmod/sentrymod навешивают slog-обёртки в Setup
    Metrics() prometheus.Registerer

    DB() *db.DB                          // nil, если Config.DB.User пуст
    HTTPC() *http.Client                 // nil до конца Setup; общий транспорт для outbound-клиентов мода
    SubjectKeyFn() func(*fiber.Ctx) string
    Context() context.Context            // долгоживущий runCtx сервиса — для фоновых воркеров мода

    NodeName() string
    ServerGroup() string
    ResolvePath(userPath, defaultName string, enabled bool) string

    AddHTTPCOption(...httpc.Option)      // работает только из Setup — HTTPC строится сразу после неё
    UseFiber(...fiber.Handler)
    AddRunOption(...fibermap.RunOption)
    AddReadinessChecker(...fibermap.Checker)
    RegisterMiddlewareFactory(name string, f FactoryFunc) // из любой фазы — Setup, Build или Wire
    WrapCronJob(func(JobFn) JobFn)        // декоратор поверх каждого cron-тика ядра + refresh-GC
    OnShutdown(func() error)
}
```

### Три фазы

```
Setup (моды) → логгер собран → DB → migrate → Auth → HTTPC
    → Build (моды) → Engine → auth-факторки → Wire (моды) → cron → готово
```

Тот же порядок, что в `service/build.go` сегодня — топосортировки между модами нет: группировка убрала межмодовые связи (натсmap живёт внутри `natsmod` вместе с NATS, а не отдельным модом со своей зависимостью), а зависимости от ядра (DB, Auth, Engine) гарантированы порядком фаз.

- **`Setuper`** — до того, как логгер и HTTP-клиент осели на `Service`. Место для `otelmod`/`sentrymod`: они навешивают обёртки на логгер (`Host.SetLogger`) и транспорт (`Host.AddHTTPCOption`) раньше, чем эти вещи попадут в руки следующим модам.
- **`Builder`** — сетевые клиенты и рантаймы поверх DB/Auth. `s3mod` живёт здесь.
- **`Wirer`** — после того как `Engine` собран: регистрация YAML middleware factories, монтирование дополнительных роутов через `Host.AddRunOption`. `Host.RegisterMiddlewareFactory` работает из любой из трёх фаз — ядро вычитывает накопленные регистрации дважды: сразу после сборки `Engine` и ещё раз после цикла `Wire`, так что мод вроде будущего `ratelimitmod` (в v1 `rate_limit_redis` монтируется именно после того, как движок построен) не должен ничего знать про эту деталь реализации.

### Идентичность и ошибки

`Name()` уникален в пределах одного `New` — дубликат ловится `CodeModDuplicate` до единого сетевого вызова. Второй экземпляр того же мода — легален через `WithName`:

```go
primary := s3mod.New(cfg.S3)
backup  := s3mod.New(cfg.S3Backup, s3mod.WithName("s3-backup"))
svc, err := svckit.New[App, Claims](ctx, cfg.Config, primary.Option(), backup.Option())
```

Ошибка мода оборачивается ядром в `*errs.Error` со стабильным кодом фазы (`svckit_mod_setup_failed` / `_build_failed` / `_wire_failed`) и именем мода; собственный код мода (`s3mod_connect_failed`) остаётся в цепочке через `Cause` — `errors.Is`/`errsval` продолжают работать. Ошибка на любой фазе сворачивает уже построенные моды через накопленный `Host.OnShutdown`, в LIFO-порядке.

## Конфигурация

`svckit.Config` — это только ядро: `Service`, `DB`, `Auth`, `HTTPC`, `Routes`. Секции опциональных подсистем не существует в ядре — приложение собирает свой конфиг встраиванием:

```go
type AppConfig struct {
    svckit.Config
    S3 s3mod.Config `envPrefix:"S3_"`
}

var cfg AppConfig
env.Parse(&cfg) // один вызов, как и раньше

s3m := s3mod.New(cfg.S3)
svc, err := svckit.New[AppCtx, Claims](ctx, cfg.Config, s3m.Option())
```

## Quickstart — `svckit.New` + `s3mod`

```go
package main

import (
    "context"
    "log"

    "github.com/caarlos0/env/v11"
    "github.com/gofiber/fiber/v2"

    "github.com/theizzatbek/gokit/fibermap"
    "github.com/theizzatbek/gokit/svckit"
    "github.com/theizzatbek/gokit/svckit/mods/s3mod"
)

type AppCtx struct{ UserID string }

type AppConfig struct {
    svckit.Config
    S3 s3mod.Config `envPrefix:"S3_"`
}

func main() {
    var cfg AppConfig
    if err := env.Parse(&cfg); err != nil {
        log.Fatal(err)
    }

    s3m := s3mod.New(cfg.S3)

    svc, err := svckit.New[AppCtx, struct{}](context.Background(), cfg.Config, s3m.Option())
    if err != nil {
        log.Fatal(err)
    }
    defer svc.Close()

    svc.SetContextBuilder(func(c *fiber.Ctx) (AppCtx, error) {
        return AppCtx{}, nil
    })

    fibermap.RegisterHandler(svc.Engine, "uploads.put", func(c *fibermap.Context[AppCtx]) error {
        // s3m.Client() паникует с наводящим текстом, если S3_BUCKET не задан
        // или s3m никогда не передавался в svckit.New.
        return s3m.Client().Put(c.Ctx.Context(), "key", c.Ctx.Body())
    })

    if err := svc.Run(); err != nil {
        log.Fatal(err)
    }
}
```

Не подключили `s3mod` вообще (ни импорта, ни строчки `s3m.Option()`) — `aws-sdk-go-v2` не попадёт в бинарь. Подключили, но `S3_BUCKET` пуст — мод остаётся выключенным (`s3m.Enabled() == false`, `Status().Mods[0].Detail == nil`), `s3m.Client()` паникует с текстом, называющим ровно этот env. Подробности — [`svckit/mods/s3mod/README.md`](mods/s3mod/README.md).

## Core-аксессоры

DB, Auth и Hasher строятся самим ядром (а не модом), поэтому их пара `Must*`/`Optional*` живёт прямо в `svckit`, а не в моде:

```go
d := svc.MustDB()                 // *db.DB или паника с указанием Config.DB.User
a, ok := svc.OptionalAuth()       // (*auth.Auth[C], bool) без паники
```

Опциональные подсистемы получают ту же пару от своего мода — `s3m.Client()` / `s3m.Optional()`.

## Status / Preflight / Readiness

`Service.Status()` больше не плоская структура с полем на подсистему — ядро не знает список модов:

```go
type Status struct {
    DB, Auth, RefreshGC bool
    Cron                int
    Mods                []ModStatus // в порядке подключения
}
type ModStatus struct {
    Name    string
    Enabled bool // из мода, если он реализует Enabler; иначе true
    Detail  any  // из мода, если он реализует Statuser
}
```

`Service.Preflight(ctx) (PreflightResult, error)` и `Service.PreflightResult(ctx) PreflightResult` — та же концентрическая проверка чекеров, что в v1, но объединённая: `Preflight` возвращает структурный результат и типизированную ошибку одним вызовом вместо двух отдельных методов v1 (`service.Preflight(ctx) error` + `service.PreflightResult(ctx) PreflightResult`). `PreflightResult` экспортирован отдельно ровно затем, чтобы будущий фасад `service/` мог делегировать в него без переписывания concurrent fan-out'а.

`Readiness`/`/readyz` не меняются по сути: чекеры как были плоским списком `fibermap.Checker`, так и остаются — просто мод добавляет свой через `Host.AddReadinessChecker` вместо того, чтобы ядро знало о нём напрямую.

## Тестирование

Пустой конфиг строит `Service` только с `Engine` + `HTTPC`:

```go
svc, _ := svckit.New[struct{}, struct{}](ctx, svckit.Config{})
```

Тесты фаз/LIFO-разворота/дубликатов имён используют фейковый мод в несколько строк без testcontainers и сети (`build_test.go`) — контракт `Mod`/`Host` проверяется изолированно от любой конкретной подсистемы. Тесты моды приносят свои собственные (testcontainers для тех, что реально ходят в сеть).

## Ограничения / roadmap

- Единственный мод сегодня — [`s3mod`](mods/s3mod/README.md). `redismod`, `natsmod`, `otelmod`, `sentrymod`, `cronmapmod`, `apimapmod`, `webhooksmod` — следующие шаги того же плана, каждый отдельным PR.
- `service/` не фасад над `svckit` — пока два независимых пакета. Когда фасад появится, `service.New` продолжит весить 39 MB (по-прежнему подключает всё), а сервисы, готовые выбирать моды, перейдут на `svckit.New` напрямую.
- Ядру запрещено импортировать любую опциональную подсистему — это гарантирует тест `TestCoreDoesNotImportOptionalSubsystems`, а не соглашение на словах.

## См. также

- [`fibermap`](../fibermap/README.md) — движок, который собирает `svckit.New`
- [`auth/authmount`](../auth/authmount/README.md) — как ядро монтирует `bearer`/`require_scope`/… без `clients/ratelimit`
- [`svckit/mods/s3mod`](mods/s3mod/README.md) — первый мод, эталон для формы следующих
- [`service`](../service/README.md) — all-in-one v1, пока не фасад
