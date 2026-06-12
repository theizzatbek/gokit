# Post-v1.0.0 backlog

Снимок: 2026-06-11, сразу после tag'а `v1.0.0`. Один файл, три
секции. Цель — не свалка идей, а триаж: что реально breaking
(копится к `v2`), что additive и потенциально едет в `v1.x` как
MINOR, и что ещё не решено (decision-pending, может смайнить любой
из двух выше).

Каждый item: короткий заголовок, контекст откуда он сюда попал, и
**Triage** строка с оценкой (`v2-only` / `v1.x MINOR` / `v1.x PATCH`
/ `pending decision`). Removing/closing — просто стираем строку и
коммитим, с одно-строчным rationale в commit message.

> **Related:** [`docs/v1-followup-licensekit.md`](v1-followup-licensekit.md)
> — first-integrator (LicenseKit) friction report received 2026-06-12.
> Most items there are `v1.0.1` patches / `v1.1.0` minor (NOT v2). One
> pending decision in §3 below — `RegisterHandlerWith*` family — closed
> as a side-effect of triaging that report.

---

## 1. Breaking candidates (v2)

То, что нельзя сделать additive'ом — меняет signature, ломает
call-sites, переименовывает что-то существующее. Накапливаем до
момента когда суммарная польза оправдает `v2.0.0` cut + migration
guide.

### service/ — split на lite vs full

Сейчас `service/` — почти-всё: DB, Auth, NATS, APIMap, HTTPC,
Engine, Cron, sentrykit, otelkit, metrics. Для каллеров которым
нужна треть субсистем — это много nil-полей и nil-checks (частично
закрыто в v1 через typed `MustX`/`OptionalX` accessors, но конструктор
всё ещё толстый). Гипотеза для v2: `service/lite/` — минимальный
wiring (Engine + Errors + optional Logger), `service/full/` —
текущий набор. Каллер выбирает по pattern'у на старте.

- **Источник:** [`docs/v1-readiness.md`](v1-readiness.md) § Open
  questions для `v1.0.0` owner-call, item #4. В v1 явно отложено.
- **Triage:** `v2-only` — Импортный путь сменится для существующих
  каллеров `service.New`. Migration guide обязателен.
- **Discovery:** нужно посмотреть на real-world `examples/`
  caller-shape — какие subsystems чаще включаются вместе, какие
  редко. Возможно lite/full недостаточно, нужен третий tier.

### Discovery sweep — findings (2026-06-11)

Manual review через `grep -E "// (TODO|FIXME|Deprecated):"` +
`grep -E "back-compat|legacy|for now"` по `*.go` + чтение
наиболее «жирных» config-структур. Поднялось семь конкретных
back-compat defaults / wrapper'ов, явно помеченных авторами для
revisit'а на следующем major'е:

#### batch.Config.MaxPending default 0 (= unbounded)
[`batch/config.go:40-42`](../batch/config.go) — комментарий
«= unbounded (back-compat — but watch for unbounded growth on
slow HandlerFn + fast Submit rates)». Silent unbounded memory
default — footgun. v2: defensive default (например, `BatchSize × 10`
или фикс `1000`), opt-in 0 для тех кто реально хочет «бесконечно».
- **Triage:** `v2-only` — change of default semantics ломает callers,
  которые молча росли на default'е (а такие, по определению, есть).

#### batch.Config.MaxInFlightHandlers default 1 (= sequential)
[`batch/config.go:44-48`](../batch/config.go) — «Default 1
(sequential — back-compat)». Современный batched dispatcher
ожидает parallel по умолчанию. v2: `0` → автодетект по
`runtime.NumCPU()` либо `2` как разумный safe baseline. Sequential
оставить как явный `=1`.
- **Triage:** `v2-only` — поведение существующих pipeline'ов
  изменится (ordering, throughput).

#### cronmap.Job.MaxRetries default 0 (= no retry)
[`cronmap/spec.go:28-34`](../cronmap/spec.go) — «MaxRetries (0 =
no retry — back-compat)». Cron job'ы по типу всегда хотят
retries на transient ошибках; silent no-retry default = тихие
проваленные cron'ы. v2: дефолт `3` (с `RetryBackoff` дефолтом
`30s`), `-1` явное «no retries».
- **Triage:** `v2-only` — изменение поведения существующих cron
  job'ов; downstream метрики `cron_attempts_total` поедут вверх.

#### fibermap.RequestLogger — collapse с RequestLoggerWithOptions
[`fibermap/reqlog.go:18, 39-58`](../fibermap/reqlog.go) — два
конструктора для одного middleware: `RequestLogger(logger,
skipPaths ...)` (back-compat) и `RequestLoggerWithOptions(logger,
opts ...RequestLoggerOption)`. Первый внутри вызывает второй.
v2: один constructor `RequestLogger(logger, opts ...Option)`,
`skipPaths ...string` уходит → `WithReqLogSkipPaths(paths ...)`.
- **Triage:** `v2-only` — signature change на call-sites.

#### db.readPoolEntry "standby" label под single-replica
[`db/db.go:27-32`](../db/db.go) — single-replica entry name'ится
`"standby"`, multi-replica — `"standby-1"`/`"standby-2"`/….
Асимметрия в метрике-labels: каллеры с alerting'ом на
`pool="standby"` ломаются при добавлении второго replica. v2:
всегда `"standby-1"` (даже single-replica), и в downstream
alerting'е есть один format.
- **Triage:** `v2-only` — Prometheus label change = breaking
  per [`docs/versioning.md`](versioning.md) § Metric names and
  label values.

#### service.resolvePath wraps resolvePathInDir
[`service/paths.go`](../service/paths.go) — `resolvePath` —
back-compat wrapper над `resolvePathInDir` с пустым
`configsDir`. Два конструктора для одного behavior'а.
v2: один экспортированный helper с `ConfigsDir` явно (либо
nil → CWD), wrapper удалить.
- **Triage:** `v2-only` — но это internal helper (lowercase), так
  что breaking только для kit'ового внутреннего кода. Низкий impact.

#### clients/cache.For[T] panics on config error
[`clients/cache/cache.go:212-224`](../clients/cache/cache.go) —
convenience constructor `For[T](rc, keyPrefix) *Redis[T]` молча
`panic(err)` если validation'а cfg фейлится. Kit-convention'ом
production-функции возвращают `*errs.Error`. v2: либо переименовать
в `MustFor[T]` (явный panic-suffix), либо вернуть error parallel
к `New[T]`.
- **Triage:** `v2-only` — signature change.

#### NewWorker shape divergence across packages
Три экспортированных `NewWorker` с разными signatures для трёх
worker-subsystems:

- [`clients/webhooks/worker.go:196`](../clients/webhooks/worker.go) —
  `NewWorker(cfg WorkerConfig) (*Worker, error)` — Config struct only
- [`db/jobs/worker.go:117`](../db/jobs/worker.go) —
  `NewWorker(d *db.DB, opts ...WorkerOption) (*Worker, error)` —
  required dep + опции
- [`db/outbox/worker.go:274`](../db/outbox/worker.go) —
  `NewWorker(d *db.DB, fn PublishFn, opts ...WorkerOption) (*Worker, error)` —
  required dep + required fn + опции

Каллер прыгающий между worker'ами кит'а каждый раз вспоминает
который из паттернов в этом подпакете. v2: нормализовать на один
shape (вероятнее всего `Config struct {ReqA, ReqB ...; OptC,
OptD ...}` + `(*Worker, error)`), переименовать или передвинуть
fields из `opts ...` в Config.

- **Источник:** discovery sweep 2026-06-11 #2.
- **Triage:** `v2-only` — signature change на всех трёх call-sites.

---

## 2. Additive / operational (v1.x MINOR / PATCH)

Не breaking. Едут в v1.x по мере того как руки доходят. Каждый
может уехать в свой PR или собраться в bucket (как делали для P2).

### CI: drift-detection для integration-matrix

При добавлении нового container-using пакета его легко забыть
прописать в `matrix.packages` нужного shard'а — пакет молча
выпадет из CI. Pre-step grep'ает `*_test.go` файлы с
testcontainers-import и сверяет union со списком в matrix YAML;
fail если drift.

- **Источник:** session 2026-06-10 (CI matrix-split), явно deferred
  в [`docs/superpowers/specs/2026-06-10-ci-matrix-split-design.md`](superpowers/specs/2026-06-10-ci-matrix-split-design.md)
  § "Drift detection — deferred".
- **Triage:** `v1.x PATCH` — workflow infra, не API.

### CI: container-reuse TestMain pattern в clients/redis, clients/nats, auth/refresh{pg,redis}

Образец уже есть — `db/testdb.BootCluster` (`cf29755`). Раскатать
тот же подход на packages где тесты по очереди bootstrap'ят свой
контейнер. Реальный выигрыш в integration-matrix wall'е (особенно
для `pg` shard'а).

- **Источник:** session 2026-06-10 («Реально полезный middle-ground
  для прода — testcontainer reuse»).
- **Triage:** `v1.x PATCH` — тестовая инфра, не API.

### CI: coverage baseline

`go test -cover` ни разу не прогонялся в CI. Добавить
`-coverprofile` собирание в `unit` job'е + aggregation в финальный
`coverage.txt`. Pin как required check с минимальным
threshold'ом — на новых пакетах целиться в 80%+.

- **Источник:** [`docs/v1-readiness.md`](v1-readiness.md) § «Что НЕ
  в audit'е».
- **Triage:** `v1.x MINOR` — публичный coverage badge меняет PR-flow
  ожидания, но не API.

### CI: benchmark baseline

Hot-paths без bench: `fibermap.Engine.Handle`, `db.Query` через
tracer, `breaker.Allow`. Добавить `go test -bench=. -benchmem` для
этих в `bench.yml` workflow + сохранять results на artifact'ах для
сравнения между PR'ами (через `benchstat`).

- **Источник:** [`docs/v1-readiness.md`](v1-readiness.md) § «Что НЕ
  в audit'е».
- **Triage:** `v1.x MINOR` — additive workflow.

### CI: apidiff в CHANGELOG

`apidiff` (или `gorelease`) против предыдущего тэга → generate
machine-readable diff exported API, прикреплять к PR / релизу. Лечит
class «забыл записать breaking change в CHANGELOG».

- **Источник:** [`docs/v1-readiness.md`](v1-readiness.md) § «Что НЕ
  в audit'е».
- **Triage:** `v1.x MINOR` — release tooling, не API.

### Per-file `/code-review ultra` sweep

Систематический проход по touched packages — gap-list собирался по
docs/CHANGELOG/README + точечному grep'у, build invariants внутри
функций не проверялись. Выявит как мелкие bugs (в `v1.0.x`), так и
breaking-candidates для §1.

- **Источник:** [`docs/v1-readiness.md`](v1-readiness.md) § «Что НЕ
  в audit'е».
- **Triage:** `pending decision` — итог влияет и на v1.x patches, и
  на §1.

### clients/nats — NewPullSubscriptionRaw asymmetry

Push-mode subscription есть в двух flavours:
[`Subscribe[T any]`](../clients/nats/subscriber.go) (typed) и
[`SubscribeRaw`](../clients/nats/subscriber.go) (raw bytes).
Pull-mode имеет только typed
[`NewPullSubscription[T any]`](../clients/nats/pull.go) — raw
варианта нет. Если consumer хочет parse'ить payload вручную в
pull-mode (например, decode'ит схему динамически), приходится
дёргать typed-вариант с `[]byte` параметром, что некрасиво.

- **Источник:** sweep 2 (subscribe surface check).
- **Triage:** `v1.x MINOR` — pure additive новый exported helper.

---

## 3. Open design questions

Решения, которые не блокируют ни одну ветку, но нужны до того как
соответствующая фича стабилизируется. Закрытие любого из этих
вопросов вытолкнёт item в §1 или §2.

### bulkhead — Gradient2 controller рядом с Vegas

`bulkhead.Controller` интерфейс exported в v1. Сейчас shipped
только `AIMDController` и `VegasController` (`adab809`). CHANGELOG
упоминал Gradient2 как «later behind the same shape». Стоит ли его
делать в v1.x — зависит от того, есть ли реальные сценарии где Vegas
проигрывает.

- **Источник:** [`docs/v1-readiness.md`](v1-readiness.md) audit item
  #2.14 (закрыт через Vegas), session обсуждение controllers.
- **Triage:** `pending decision` — если делаем, это `v1.x MINOR`
  (additive controller, тот же интерфейс).

### Webhook tracing — альтернативные propagator'ы (B3, Datadog)

В v1 зафиксировано: W3C TraceContext — единственный
tracing-injection контракт (`d9139e9`). Если consumer'ы кит'а
интегрируются с Datadog APM, у них B3-propagator стек. Стоит ли
делать `WorkerConfig.Propagator` pluggable interface'ом, и где
проходит грань между «pluggable» и «обещаем стабильность W3C»?

- **Источник:** [`docs/v1-readiness.md`](v1-readiness.md) audit item
  #2.11 (закрыт фиксацией W3C на v1).
- **Triage:** `pending decision` — если pluggable, это либо
  `v1.x MINOR` (новая Option без breaking) либо `v2-only`
  (зависит от того как меняем default).

### Multi-replica routing — расширения

`db.ReadPools()` сейчас отдаёт всех с lag/healthy. Но routing
стратегий пока две (round-robin / random). Что добавить: lag-aware
(всегда меньший lag), zone-aware (топология), weighted? Не понятно
есть ли real-world давление на этот выбор.

- **Источник:** `db/db.go` routing-decision код (`pickReadPool`).
- **Triage:** `pending decision` — additive Option, `v1.x MINOR`.

### Sessions store API — какие backend'ы ещё ждать

Сейчас два store'а: `MemoryStore` (dev) и `sessionsredis.Store`
(prod). Есть ли давление на Postgres-backed store, или Redis +
in-memory покрывают use-cases? Если PG нужен — что в Stats?

- **Источник:** v1 audit-discussion про
  `auth/sessions.StoreStats.Expired` (закрыто).
- **Triage:** `pending decision` — additive backend, `v1.x MINOR`.

### breaker.OpenIntervalMultiplier default

[`breaker/config.go:68-76`](../breaker/config.go) — `Default 1.0`
= constant `OpenInterval` без экспоненциального роста. Безопасный
дефолт, но смысл feature теряется (надо явно ставить `Multiplier
> 1`). Стоит ли в v2 дефолтить в `2.0` (классический exponential
backoff) или оставить consciously-off? Решение зависит от
типичного use-case киrt'а: short-trip breaker'ы хороши с
constant'ом, long-recovery — с exponential.

- **Источник:** discovery sweep 2026-06-11.
- **Triage:** `pending decision` — итог либо в v2 (default change
  = MAJOR per metric/behaviour rules в versioning.md), либо
  остаётся как есть.

### clients/nats default ack classifier

[`clients/nats/subscriber.go:381-395`](../clients/nats/subscriber.go) —
`defaultClassifier` хардкодит контракт `nil→Ack`, `ErrPoison→Term`,
всё остальное → `Nak`. Сейчас уже configurable через `Option`,
вопрос — нужно ли в v2 более sophisticated default (например,
автоматическая Term на `context.Canceled` чтобы не Nack'ать
shutdown'ы), или kept-as-is плюс документирование когда писать
свой Classifier.

- **Источник:** discovery sweep 2026-06-11.
- **Triage:** `pending decision` — если меняем default, это
  `v2-only` (behaviour change по docs/versioning.md § Behavioural
  changes that aren't signature changes).

### auth.APIKeyFactory — declarative header overrides

[`auth/apikey.go:398-409`](../auth/apikey.go) — author явно решил
оставить header/query overrides программными, не YAML-декларативными
(«declarative tends to invite inconsistent header names across
routes»). Аргумент валидный, но если consumer'ы кит'а реально
хотят per-route header'ы (например, `X-Internal-API-Key` для
internal endpoints vs `Authorization: Bearer` для public), это
ограничение становится больно. v2 candidate если давление
появится.

- **Источник:** discovery sweep 2026-06-11.
- **Triage:** `pending decision` — пока нет real-world давления,
  default keeps. Если давление появится → `v1.x MINOR` (additive
  Option) или `v2-only` если меняем YAML-schema.

### Constructor convention — Config struct vs Options pattern

Kit смешивает два паттерна для constructor'ов:

- **Config struct only** — `clients/webhooks.NewWorker(cfg
  WorkerConfig)`, `clients/webhooks.NewFanout(cfg FanoutConfig)`,
  `clients/webhooks/storepg.NewDeliveryStore(q, secretKey)`,
  `clients/webhooks/verifiers.NewGenericHMAC(cfg GenericHMACConfig)`,
  `auth/refresh{pg,redis}.NewStore`, `sentrykit.Init`.
- **Variadic Options** — `clients/redis.Connect(ctx, cfg, opts...)`,
  `clients/nats.Connect(ctx, cfg, opts...)`,
  `db/jobs.NewWorker(d, opts...)`,
  `db/outbox.NewWorker(d, fn, opts...)`,
  `fibermap.RequestLogger`/`RequestLoggerWithOptions`.
- **Hybrid (Config + opts)** —
  `clients/ratelimit.NewRedis(rc, cfg, opts...)`,
  `clients/httpc.NewTransport(cfg, opts...)`.

Hybrid читаемо: required → Config struct fields; tunables → opts.
Чистый options-only паттерн страдает когда required deps растут
(см. `db/outbox.NewWorker(d, fn, opts...)` — два positional'а уже).
Чистый Config-only паттерн страдает growth-через-добавление-полей
(добавление optional field = breaking если кто-то использует
field-order init).

Вопрос для v2: договориться что hybrid — kit-стандарт, и
нормализовать выпадающие call-sites. Или оставить как есть и
просто документировать convention.

- **Источник:** sweep 2 (constructor signature survey).
- **Triage:** `pending decision` — если settle на hybrid,
  outliers (webhooks Worker, fanout, refresh* stores, jobs Worker,
  outbox Worker, ratelimit) приедут в `v2` отдельным bundle'ом.

### fibermap.RegisterHandlerWith* family — 5 typed + 1 generic Input

[`fibermap/bind_register.go`](../fibermap/bind_register.go) —
пять типизированных variants
(`RegisterHandlerWithBody/Query/Params/Headers`) плюс
[`bind_input.go`](../fibermap/bind_input.go) с generic
`RegisterHandlerWithInput[T, Input]`. Cross-cutting question:
typed variants дают autoschema-derive (`WithBody(zero[Req]())`
forward), `WithInput` — generic multi-source через struct
с `Body`/`Params`/`Query`/`Headers` field-name convention.
Documented ergonomic split, но 5 функций для четырёх sources
читается тяжело в `go doc`.

Вопрос: оставить как есть (ergonomic shortcut для типичных
4 sources), или collapse в один `RegisterHandlerTyped[T, Req
any](src BindSource, ...)` где `BindSource = Body|Query|Params|Headers`
explicit'ной?

- **Источник:** sweep 2 (fibermap public surface).
- **Triage:** **`decided: keep both`** — закрыто 2026-06-12.
  LicenseKit integrator friction report
  ([`docs/v1-followup-licensekit.md`](v1-followup-licensekit.md))
  предложил «новый» `WithInput`, не заметив существующего; это
  подтверждает что `WithInput` discoverability страдает, но не
  что single-source helpers лишние. Followup-doc D-3 (`v1.0.1`
  patch wave) поднимает `WithInput` в README quickstart рядом с
  single-source вариантами. Никакой deprecation legacy 5 не
  планируется до v2 — оба паттерна остаются.

---

## Triage cheat-sheet

| Tag | Что это значит |
|---|---|
| `v2-only` | Действительно breaking. Копится. Не делаем в v1.x. |
| `v1.x MINOR` | Additive (новый exported symbol / Option / env var). MINOR bump. |
| `v1.x PATCH` | Internal / infra / docs-only. PATCH bump. |
| `pending decision` | Нужно принять решение до того как item уедет в одну из категорий выше. |

Removing an item: `git rm` строку, commit с одно-строчным
обоснованием (closed / superseded / no-longer-relevant). История —
в `git log` этого файла.
