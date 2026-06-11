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

### Discovery sweep — что ещё всплыло как «нелогично»

Никто не делал post-v1 per-file code-review. Скорее всего там
накопились мелкие inconsistency: имена методов, порядок аргументов,
typed errors vs sentinel, etc. До per-file sweep'а конкретики тут
нет — это placeholder напоминание провести review до того как v2
content-list закроется.

- **Источник:** [`docs/v1-readiness.md`](v1-readiness.md) § «Что НЕ
  в audit'е», bullet про `/code-review ultra`.
- **Triage:** `pending decision` — итог sweep'а определит, что-то
  попадёт в v2, что-то в v1.x.

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
