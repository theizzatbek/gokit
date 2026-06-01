# otelkit

Тонкий OpenTelemetry tracing bootstrap для kit-based сервисов. Один
вызов настраивает TracerProvider, экспортирующий через OTLP/HTTP,
подключает его как process-global tracer + propagator и возвращает
shutdown-функцию, которую caller'ы регистрируют в своём cleanup-пути.

**Импорт:** `github.com/theizzatbek/gokit/otelkit`
**Зависит от:** `go.opentelemetry.io/otel/{sdk,exporters/otlp/otlptrace/otlptracehttp,propagation,...}`

## Quickstart

```go
shutdown, err := otelkit.Setup(ctx, "urlshort",
    otelkit.WithServiceVersion("1.0.0"),
    otelkit.WithSampleRatio(0.1),
    otelkit.WithResourceAttribute("deployment.environment", "production"),
)
if err != nil { return err }

defer func() {
    sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    _ = shutdown(sctx)
}()
```

Для типичного kit-сервиса `service.WithOtel(serviceName, ...)` оборачивает это плюс otelfiber + otelhttp transport одним вызовом — см.
[service README](../service/README.md).

## Конфигурация

OTLP/HTTP экспортер читает стандартные OTel environment-переменные —
никаких kit-specific knob'ов:

| Env | Назначение |
|---|---|
| `OTEL_EXPORTER_OTLP_ENDPOINT` | Endpoint коллектора (напр. `http://otel-collector:4318`) |
| `OTEL_EXPORTER_OTLP_HEADERS` | Дополнительные заголовки (auth-токены, tenant id) |
| `OTEL_EXPORTER_OTLP_COMPRESSION` | `gzip` или `none` |
| `OTEL_RESOURCE_ATTRIBUTES` | Resource-атрибуты, мерджащиеся в каждый span |

Для значений, которые кит читает напрямую:

| Опция | По умолчанию | Заметки |
|---|---|---|
| `Setup(ctx, serviceName)` | — | Обязательна. Заполняет `service.name`. Пустое значение возвращает ошибку. |
| `WithServiceVersion(v)` | "" | Устанавливает `service.version` на ресурсе |
| `WithSampleRatio(r)` | 1.0 | Head-based sampling ratio (0..1) |
| `WithResourceAttribute(k, v)` | — | Добавить константный атрибут (region, az, cluster) |
| `WithExporterOption(opt)` | — | Прокинуть `otlptracehttp.Option` для endpoint/headers из кода |

## Поведение

- **Propagation:** W3C TraceContext + W3C Baggage. Входящие запросы, несущие `traceparent`, продолжают трассу; исходящие вызовы инжектят его через `otelhttp`.
- **Sampler:** ratio-based когда < 1.0; `AlwaysSample` когда ≥ 1.0.
- **Batcher:** 5s flush window. Pending span'ы flush'атся во время `shutdown(ctx)` — bound'ите конечный deadline перед вызовом, иначе нереспонсивный коллектор блокирует indefinitely.
- **Идемпотентный shutdown:** возвращаемая функция guard'ена `sync.Once`.

## Метрики

`otelkit.SetupMetrics(ctx, serviceName, promRegistry, opts...)` открывает
вторую OTLP/HTTP-пайплайн, которая **мостит** Prometheus-коллекторы
кита на OTel periodic push. Так существующая инструментация
`db_*`/`httpc_*`/`nats_*`/`apimap_*`/`auth_*`/`fibermap_http_*`
приходит на тот же OTel-коллектор, что и трассы — без необходимости
переписывать инструментацию кита в OTel API.

```go
shutdown, err := otelkit.SetupMetrics(ctx, "urlshort", svc.Metrics().(prometheus.Gatherer),
    otelkit.WithMetricsInterval(30 * time.Second),
    otelkit.WithMetricsServiceVersion("1.0.0"),
)
```

| Опция | По умолчанию | Заметки |
|---|---|---|
| `WithMetricsServiceVersion(v)` | "" | `service.version` на metric-ресурсе |
| `WithMetricsResourceAttribute(k, v)` | — | Добавить константный атрибут (region, az, cluster) |
| `WithMetricsExporterOption(opt)` | — | Прокинуть `otlpmetrichttp.Option` для endpoint/headers из кода |
| `WithMetricsInterval(d)` | 60s | PeriodicReader push interval |

`service.WithOtel` авто-подключает `SetupMetrics` всякий раз, когда service-registry — это `prometheus.Gatherer` (дефолтный `prometheus.NewRegistry()` — да). Отключите через `service.WithoutOtelMetrics()`, когда deployment уже скрейпит `/metrics` и не хочет параллельной push-пайплайн.

## pgx tracer

`otelkit.NewPgxTracer(opts...)` возвращает `pgx.QueryTracer`, который открывает CLIENT span на каждый query, прикрепляет `db.system=postgresql` + `db.query.text` (sanitised SQL, обрезанный по умолчанию на 4096 chars) на старте и записывает результирующий статус на конце. Подключайте к `db.Connect`:

```go
pgxTracer := otelkit.NewPgxTracer(
    otelkit.WithPgxTracerName("orders-db"),
)
dbConn, _ := db.Connect(ctx, cfg, db.WithTracer(pgxTracer))
```

| Опция | Заметки |
|---|---|
| `WithPgxTracerName(name)` | Override имени tracer'а, появляющегося в метаданных instrumentation library. По умолчанию: путь kit-пакета. |
| `WithPgxSpanNamer(fn)` | Кастомный builder span-имени из SQL. По умолчанию возвращает константу `"db.query"` — PII-free, low-cardinality. |
| `WithoutPgxSQL()` | Подавить атрибут `db.query.text` (multi-tenant предикаты / audit-ограничения). |
| `WithPgxMaxSQLLength(n)` | Обрезать statement на n байт. По умолчанию 4096; 0 отключает обрезку. |

`service.WithOtel` авто-подключает это к kit-овому DB-пулу всякий раз, когда DB сконфигурирована — `service.WithOtelPgxOptions(...)` прокидывает опции, `service.WithoutOtelPgxTracer()` отключает.

## Logs

`SetupLogs(ctx, serviceName, opts...)` инициализирует OTLP/HTTP log-exporter + batch-processor + global `log.LoggerProvider`. Возвращает shutdown — отложите его LIFO рядом с tracer/meter shutdown'ом. Reads те же `OTEL_EXPORTER_OTLP_*` env'ы, что и tracer/metrics, поэтому никакой дополнительной conf'ы для general-case-deployment'а не нужно.

`SlogHandler(inner, scopeName) slog.Handler` — slog-handler-обёртка, которая tee'ит каждый `slog.Record` в `inner` И в OTel-логи через `go.opentelemetry.io/contrib/bridges/otelslog`. Inner-handler — это ваш существующий stderr/stdout-sink (`slog.NewJSONHandler(os.Stdout, …)`), OTel-сторона публикует record в configured `LoggerProvider`. Когда `SetupLogs` ещё не звался, OTel-сторона использует no-op global provider — overhead'а нет.

```go
shutdownLogs, _ := otelkit.SetupLogs(ctx, "myservice",
    otelkit.WithLogsServiceVersion("v1.2.3"))
defer shutdownLogs(context.Background())

base := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
logger := slog.New(otelkit.SlogHandler(base, "myservice"))
```

`service.WithOtel(serviceName)` авто-подключает это: kit-built logger оборачивается `SlogHandler`'ом, и каждый kit-side `slog`-вызов (db/auth/httpc/nats) автоматически шипится в OTel-коллектор без caller-wiring'а. `WithLogger`-supplied loggers leave'ятся untouched — assumption: caller уже владеет своим observability-pipeline. Подавить через `service.WithoutOtelLogs()`, тюнить через `service.WithOtelLogsOptions(otelkit.WithLogsServiceVersion(...))`.

| LogsOption | Заметки |
|---|---|
| `WithLogsServiceVersion(v)` | Установить `service.version` resource-атрибут. Default — empty. |
| `WithLogsResourceAttribute(k, v)` | Добавить произвольный resource-атрибут к каждому log-record'у. Чанишь по нескольку раз для multiple key/value-пар. |
| `WithLogsExporterOption(otlploghttp.Option)` | Прокинуть raw-`otlploghttp` option (custom-endpoint, TLS-config, retry). |

## Ограничения

- **Только OTLP/HTTP для logs.** Никакого gRPC-exporter'а — та же причина, что и у tracer/metrics-сторон.
- **Только OTLP/HTTP для traces/metrics.** Никакого gRPC-экспортера (добавил бы `google.golang.org/grpc` в прямые зависимости). Подключите руками через `WithExporterOption` / `WithMetricsExporterOption`, если очень нужно.
- **Нет SDK-level кастомизации.** Стек SpanProcessor зафиксирован на одном Batcher; metric-пайплайн зафиксирован на одном PeriodicReader. Для multi-pipeline конфигураций конструируйте свой `TracerProvider` / `MeterProvider` и зовите `otel.SetTracerProvider` / `otel.SetMeterProvider` напрямую.

## См. также

- [`service`](../service/README.md) — `WithOtel(serviceName, ...)` подключает otelkit + otelfiber + otelhttp в одной опции
- [`clients/httpc`](../clients/httpc/README.md) — outbound HTTP transport, который otelhttp оборачивает через `WithBaseTransport`
- [`fibermap`](../fibermap/README.md) — inbound routing-слой; middleware otelfiber монтится на уровне App
</content>
