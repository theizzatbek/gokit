# sentrykit

Тонкий Sentry error-tracking bootstrap для kit-based сервисов. Один
вызов инициализирует Sentry SDK и возвращает flush-функцию;
companion [`FiberMiddleware`](#fibermiddleware) клонирует per-request
hub и auto-capture'ит panics; [`WrapErrorHandler`](#wraperrorhandler)
превращает 5xx-ответы в Sentry-события.

**Импорт:** `github.com/theizzatbek/gokit/sentrykit`
**Зависит от:** `github.com/getsentry/sentry-go`

## Quickstart

```go
shutdown, err := sentrykit.Setup(ctx, dsn,
    sentrykit.WithEnvironment("production"),
    sentrykit.WithRelease("svc@1.0.0"),
    sentrykit.WithTag("region", "us-east-1"),
)
if err != nil { return err }

defer func() {
    sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    _ = shutdown(sctx)
}()
```

Для типичного kit-сервиса `service.WithSentry(dsn, ...)` оборачивает
Setup + FiberMiddleware + shutdown-проводку одной опцией — см.
[service README](../service/README.md).

## Опции

| Опция | По умолчанию | Заметки |
|---|---|---|
| `Setup(ctx, dsn)` | — | dsn обязателен; пустое значение возвращает ошибку |
| `WithEnvironment(env)` | "" | Тэг `environment` на каждом событии (production/staging/dev) |
| `WithRelease(release)` | "" или [AutoRelease](#детекция-release), когда используется service.WithSentry | Тэг `release` — git SHA, image tag, semver. Override'ит любое значение, которое подобрал AutoRelease. |
| `WithSampleRate(r)` | 1.0 | Доля error-событий, отправляемых (0..1) |
| `WithTracesSampleRate(r)` | 0 | Sentry-native transactions. Держите на 0 при использовании otelkit для tracing'а. |
| `WithBeforeSend(fn)` | nil | Hook для очистки PII, drop'а шумных событий, прикрепления extras |
| `WithDebug(bool)` | false | Sentry SDK debug-логи в stderr (только local-setup) |
| `WithFlushTimeout(d)` | 5s | Default deadline, используемый shutdown'ом, когда у ctx нет своего |
| `WithServerName(name)` | auto | Override'ит авто-определённый hostname-тэг |
| `WithTag(key, value)` | — | Pre-populate тэга, попадающего на каждое событие |

SDK читает `SENTRY_DSN` и friends из окружения, когда они не установлены
в коде — та же конвенция, что и у остального кита.

## Детекция Release

`sentrykit.AutoRelease()` резолвит release-тэг без конфигурации со
стороны caller'а. Priority-цепочка:

1. Env-переменная `SENTRY_RELEASE`.
2. `OTEL_RESOURCE_ATTRIBUTES` `service.version=X` (так что одно
   объявление `service.WithOtel(svc, otelkit.WithServiceVersion(v))`
   питает обе пайплайны).
3. `runtime/debug.ReadBuildInfo().Main.Version`, когда оно не
   `(devel)` — обычно semver из `go install <module>@vX.Y.Z`.
4. `vcs.revision` build-setting, обрезанный до 12 chars (матчит
   short-SHA конвенцию, которую Sentry использует для unversioned
   коммитов).
5. Пустая строка — sentry-go это принимает и shipping'ит события без
   release-attribution.

`service.setupSentry` prepend'ит `WithRelease(AutoRelease())` к
caller'скому списку `sentrykit.Option`, так что explicit
`sentrykit.WithRelease(...)` от caller'а всё ещё побеждает через
last-write-wins.

Для untrimmed local `go run` сборок (без vcs-метаданных), установите
`SENTRY_RELEASE=local-dev` в своём shell'е, чтобы избежать отправки
событий без attribution; production-сборки с `go build -trimpath`
авто-подхватывают vcs-revision.

## FiberMiddleware

```go
app.Use(sentrykit.FiberMiddleware())
```

На каждый запрос:

1. Клонирует `sentry.CurrentHub()` в request-scoped `*sentry.Hub`.
2. Populate'ит scope hub'а HTTP-контекстом: method, headers, IP,
   request_id (когда [`fibermap.RequestID`](../fibermap/README.md)
   upstream).
3. Хранит hub в `c.Locals(hubKey{})` — читается через
   `sentrykit.HubFromContext(c)`.
4. Defer'ит `recover()`: на panic capture'ит exception с
   request-scope'ом и **re-panic'ит**. Внешний `fibermap.Recover`
   всё ещё пишет 500-ответ — никакого изменения поведения для wire.

`http.route` (Fiber route-template, например, `/users/:id`) лениво
прикрепляется: Fiber резолвит matched-route только после того, как
глобальная middleware-цепочка продвинется мимо `FiberMiddleware`,
так что тэг устанавливается либо когда handler зовёт
`HubFromContext`, либо в deferred-panic пути до того, как запустится
`RecoverWithContext`.

## HubFromContext

```go
sentrykit.HubFromContext(c).CaptureException(err)
sentrykit.HubFromContext(c).Scope().SetUser(sentry.User{ID: subject})
```

Возвращает request-scoped hub, сохранённый `FiberMiddleware`'ой.
Fallback'ится на `sentry.CurrentHub()` (process-global), когда
middleware не в цепочке — caller'ы всегда могут эмитить, они просто
теряют request-scoped тэги.

## WrapErrorHandler

```go
app := fiber.New(fiber.Config{
    ErrorHandler: sentrykit.WrapErrorHandler(fibermap.ErrorHandler(logger)),
})
```

Capture'ит переданную error в per-request hub, когда `errs.HTTP(err)`
резолвится в статус >= 500, потом делегирует в `inner`. 4xx-ошибки
проходят без изменений — validation-failures и auth-rejections
по умолчанию не Sentry-worthy.

`service.WithSentry` НЕ auto-wrap'ит, потому что не каждый сервис
устанавливает кастомный error-handler. Подключите явно через
`service.WithRunOptions(fibermap.WithFiberConfig(...))`.

## Subsystem breadcrumbs (slog мост)

`sentrykit.SlogHandler(inner, opts...)` оборачивает любой
`slog.Handler`, так что каждая log-запись, проходящая через него,
также становится Sentry-breadcrumb'ом на request-hub'е. Inner-handler
всё ещё получает каждую запись — console / JSON-логирование
продолжает работать.

```go
inner := slog.NewJSONHandler(os.Stdout, nil)
logger := slog.New(sentrykit.SlogHandler(inner,
    sentrykit.WithAttrFilter(func(k string) bool { return k != "sql" }),
    sentrykit.WithMaxBreadcrumbValueLen(256),
))
```

Резолв hub'а: `sentry.GetHubFromContext(ctx)` сначала, потом
`sentry.CurrentHub()`. `FiberMiddleware` кладёт request-hub на
`c.UserContext()` (через `sentry.SetHubOnContext`), так что любой
subsystem-логгер, использующий `*Context` варианты, автоматически
подхватывает правильный per-request hub — pgx-tracer от db
(`LogAttrs(ctx, ...)`), security-логгер от auth
(`InfoContext(c.UserContext(), ...)`), retry-лог от httpc
(`WarnContext(req.Context(), ...)`) — все qualify out of the box.

Маппинг уровней:

| slog | Sentry breadcrumb-level | По умолчанию |
|---|---|---|
| Debug | "debug" | **пропущено** (используйте `WithDebugBreadcrumbs`) |
| Info | "info" | on |
| Warn | "warning" | on |
| Error | "error" | on (только breadcrumb — `CaptureException` опциональный) |

Debug пропущен по умолчанию, потому что pgx-query-tracer логирует
каждый успешный query на Debug-уровне — пуская их, мы залили бы
100-item breadcrumb-буфер внутри одного transactional-handler'а.

Резолв категории: explicit attr (default-ключ `category`) → first
word/`:`-prefix лоуэркейснутого message → literal `"log"`. Так что
`logger.Info("httpc retry", ...)` падает как `category="httpc"`
без изменений в коде.

| Опция | По умолчанию | Заметки |
|---|---|---|
| `WithDebugBreadcrumbs()` | off | Включить Debug-логи |
| `WithCategoryAttr(key)` | "category" | Slog-attr для promote в breadcrumb.Category |
| `WithMaxBreadcrumbValueLen(n)` | 512 | Cap stringified attr-значений; n ≤ 0 отключает |
| `WithAttrFilter(fn)` | nil | Дропать attr-ключи, для которых fn возвращает false |
| `WithCaptureLevel(level)` | off | Записи ≥ level capture'ятся как Sentry-события (в дополнение к breadcrumb) |
| `WithCaptureErrorAttrKeys(keys...)` | err / error / cause | Attr-ключи, consult'ируемые для `error`-значения → CaptureException; иначе CaptureMessage |
| `WithCaptureDedupeWindow(d)` | 60s | Подавить duplicate-события для того же `(level, category, message)` внутри d; 0 отключает |

### Error → event auto-capture

`WithCaptureLevel(slog.LevelError)` превращает handler в Sentry-sink
для high-severity log-записей:

```go
sentrykit.SlogHandler(inner,
    sentrykit.WithCaptureLevel(slog.LevelError),
)
```

- Записи ≥ threshold ship'ятся как события. Breadcrumb добавляется
  СНАЧАЛА (так что event-timeline включает его).
- Если attr в `err` / `error` / `cause` (конфигурируется через
  `WithCaptureErrorAttrKeys`) несёт `error`-значение, событие — это
  Sentry **Exception** (stack-frames + `error.Error()` как
  `Exception.Value`). Иначе — **Message**-событие с
  `record.Message`.
- Attrs пакуются в один `log` Sentry-context block, оставляя
  global-tag-facet чистым.
- Dedupe по fingerprint'у `(level, category, message)`. Fingerprint
  намеренно игнорирует attr-значения — тот же `db query failed`
  не должен производить свежее событие per concrete-query.

`service.WithSentryErrorCapture(slog.LevelError)` — service-level
шорткат.

`service.WithSentry` auto-wrap'ит kit-built логгер этим handler'ом.
User-supplied логгеры (через `service.WithLogger`) уважаются без
изменений — opt in руками, если хотите breadcrumb-coverage там.
Используйте `service.WithSentryBreadcrumbs(...)`, чтобы пробросить
handler-опции в auto-wrap путь.

## Cron monitoring

Sentry Crons превращает periodic-scheduler'ы в мониторящиеся
объекты: ожидаемое расписание + last outcome показывается в UI,
alert'ы на missing heartbeats, alert'ы на consecutive errors.
`sentrykit` выставляет три хелпера; `service.WithRefreshGC`
авто-подключает их.

```go
err := sentrykit.MonitorCronWithConfig(ctx, "kit-refresh-gc",
    sentrykit.IntervalMonitorConfig(15*time.Minute),
    func(ctx context.Context) error { return store.GarbageCollect(ctx, time.Now()) })
```

На каждое invocation: отправить `in_progress`, run fn, отправить
`ok` (nil return) или `error` (non-nil) с актуальной duration.
Check-in ID'шники сцеплены, так что Sentry пейр'ит start + end
события.

| Хелпер | Когда использовать |
|---|---|
| `MonitorCron(ctx, slug, fn)` | Monitor уже сконфигурирован в Sentry UI; вы просто хотите heartbeats. |
| `MonitorCronWithConfig(ctx, slug, cfg, fn)` | Code-defined schedule. Каждый check-in upsert'ит config, так что операторы не поддерживают его отдельно. |
| `IntervalMonitorConfig(d)` | Sensible default `MonitorConfig` для ticker-style job'ов: minute-grained schedule, checkInMargin + maxRuntime = 2×interval capped at 30. |

Когда `sentrykit.Setup` не запускался (нет DSN, нет `WithSentry`),
обёртки — прозрачный pass-through — fn запускается один раз, никакой
check-in не dispatch'ится. Это сделано, чтобы scheduler'ы могли звать
`MonitorCron(...)` безусловно без conditional-code-путей.

Panics в fn пропагируют; `ok`/`error` check-in НЕ отправляется в
этом случае. Более широкий crash-путь поднимет panic через
global-hub.

`service.WithRefreshGC` автоматически использует
`MonitorCronWithConfig("kit-refresh-gc", IntervalMonitorConfig(interval), ...)`,
когда и `WithSentry`, и `WithRefreshGC` установлены. Override slug'а
через `service.WithSentryRefreshGCSlug(...)`; отключите cron-monitoring
полностью (сохранив остальное от Sentry) через
`service.WithoutSentryRefreshGCMonitor()`.

## Таблица истины capture

| Триггер | FiberMiddleware? | WrapErrorHandler? | Capture'ится? |
|---|---|---|---|
| handler panic (error-значение) | да | n/a | да (Exception-событие) |
| handler panic (string) | да | n/a | да (Message-событие) |
| handler возвращает `errs.Internal(...)` | да | да | да |
| handler возвращает `errs.Internal(...)` | да | нет | нет (default fiber ErrorHandler без hook'а) |
| handler возвращает `errs.NotFound(...)` | да | да | нет (status < 500) |
| `HubFromContext(c).CaptureException(err)` explicit | да | n/a | да |
| `sentry.CaptureException(err)` package-level (без ctx) | любой | n/a | да (process-global hub, без request-scope'а) |

## Ограничения

- **Traces+metrics вне scope'а.** Performance принадлежит
  [`otelkit`](../otelkit/README.md); Sentry может ingest'ить их через
  OTLP, если вы направите OTel-exporter на Sentry-endpoint.
- **Нет stack-frames из обёрнутых ошибок.** Когда attr `err` несёт
  `errs.Error` с `Cause`, captured Exception использует stack
  работающей goroutine'ы — не stack, на котором был произведён
  cause. Проводка `runtime.Frame`-extractor'а в `errs.Cause` — это
  future follow-up, если нужно.
- **Нет CPU-профилирования.** `sentry-go` v0.46.2 не выставляет
  стабильную profiling-client опцию. Приземлится в follow-up'е, как
  только SDK shipping'ит `ProfilesSampleRate` (или преемник) в своих
  `ClientOptions`.
- **Нет авто-детекции release.** `WithRelease` должна передаваться
  явно (или env `SENTRY_RELEASE`). Follow-up подключит значение из
  `service.Service.NodeName` / `service.version` resource-attr.
- **Нет per-request user-scope'а из JWT.** Handler'ы могут установить
  его сами через `HubFromContext(c).Scope().SetUser(...)` —
  follow-up читает `auth.From(c)` автоматически.

## См. также

- [`service`](../service/README.md) — `WithSentry(dsn, ...)` подключает Setup + FiberMiddleware + shutdown одной опцией
- [`otelkit`](../otelkit/README.md) — performance tracing + metrics-пайплайн; когда оба on, Sentry-события шарят OTel trace_id
- [`errs`](../errs/README.md) — `errs.HTTP(err)` резолвит wire-status, используемый `WrapErrorHandler`'ом
</content>
