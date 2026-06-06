# v1 Readiness Audit

Snapshot date: 2026-06-06 (rev). Audit conducted on `main @ 29e1838` после merge'ей PR #152 (chore: v1-prep bucket A) и PR #153 (docs: v1-prep — semver policy + security disclosure).

Цель — собрать **что ещё нужно решить** перед тем как тегнуть `v1.0.0` и
зафиксировать API-стабильность. Каждый item помечен приоритетом:

- **P0** — blocker для v1. Решить ДО v1.0.0.
- **P1** — желательно к v1, но можно вынести в v1.1.
- **P2** — nice-to-have, не критично.

Снизу — рекомендованный порядок merge'ей.

## Закрыто с первой ревизии

Repo-wide policy-bucket целиком ушёл в PR #152 + PR #153 + готовится merge `chore/v1-drop-docs-packages`:

- ✓ #1.1 semver-policy → [`docs/versioning.md`](versioning.md) (PR #153)
- ✓ #1.2 LICENSE copyright → `gokit contributors` (PR #152)
- ✓ #1.3 [`SECURITY.md`](../SECURITY.md) (PR #153)
- ✓ #1.4 `cmd/kit/gen_db_cluster.go` → `bitnamilegacy/postgresql:16` (PR #152)
- ✓ #1.5 Go-версия README ↔ go.mod синхронизированы на `1.26.4` (PR #152)
- ✓ #1.6 go.mod bump до `1.26.4` (PR #152)
- ✓ #1.7 [`CONTRIBUTING.md`](../CONTRIBUTING.md) (PR #152)
- ✓ #1.8 Decision Guide вынесен в [`docs/decision-guide.md`](decision-guide.md) (PR #152)
- ✓ #1.9 → **снят с повестки**: `docs/packages/` дропается в `chore/v1-drop-docs-packages`, canonical-источник API-контрактов — `<subpkg>/README.md` + `doc.go` рядом с кодом

---

## 1. Repo-wide — остаток

### P1 — желательно

| # | Item | Где | Почему |
|---|---|---|---|
| 1.10 | **Run `go test -race -count=1 ./...` в integration job** | `.github/workflows/test.yml:127-130` | Сейчас явно отключено комментарием "No -race here". Race-prone code в core покрыт `unit-race`, но integration без race-детектора — слепое пятно для testcontainers-based кода (`db/testdb`, `clients/nats`, refresh-stores). |

### P2 — nice-to-have

| # | Item | Где | Почему |
|---|---|---|---|
| 1.11 | **`CODE_OF_CONDUCT.md`** | repo root | Стандарт для public OSS, но не обязателен для kit'а. |
| 1.12 | **GitHub issue templates** | `.github/ISSUE_TEMPLATE/` | Bug report + feature request templates снижают шум в issues. |
| 1.13 | **CHANGELOG.md уже 63k chars** | `CHANGELOG.md` | После v1 хорошо бы архивировать pre-v1 историю в `docs/CHANGELOG-0.x.md` и держать в основном файле только v1+. |

---

## 2. Per-subpackage gaps

API-контракты живут в `<subpkg>/README.md` и `<subpkg>/doc.go` — туда же
прицеливаемся при дописывании документации.

### `errs/` — **готов**

- API минимальный и stable (Kind/Code/Details/Cause).
- Stdlib-only, без deps drift.
- **P2:** `errs/errsval/` мог бы получить `validator.RegisterTranslation` для локализации сообщений, но это OOS для kit'а.

### `db/`

| # | Item | Приоритет |
|---|---|---|
| 2.1 | `db.HasReadReplica()` и `db.ReadPool()` помечены «back-compat with the previous single-pool surface» (`db/db.go:65, :460`). Перед v1 решить — оставить или снести. | **P0** — лучше снести: они в README как escape-hatches, но новый код должен идти через `ReadPools()` / `ReadQuery`. |
| 2.2 | `db/testdb` всё ещё бутстрапит cluster каждый тест (комментарий «Cluster bootstraps are ALWAYS per-call»). Если v1 обещает testdb как stable testing API — это performance-trap для пользователей. | **P1** — задокументировать ярче, либо подложить cluster-reuse за optin. |
| 2.3 | `db/sqb/` — squirrel зависимость pinned to 1.5.4 (последняя). Если squirrel сделает 2.0 — придётся думать. | **P2** — мониторить только. |
| 2.4 | `db/migrate.Generate` — `WithTimestamp()` vs `WithNext()` режимы. Если оба останутся — задокументировать когда какой использовать. | **P2** — кажется уже задокументировано в CHANGELOG. |

### `auth/`

| # | Item | Приоритет |
|---|---|---|
| 2.5 | `auth.SetPrincipalForTest[C]` — public test-helper в production-package (`auth/context.go:76`). Suffix `ForTest` это marker, но коллеги могут вызвать в проде. | **P1** — вынести в `auth/authtest/` subpackage; production-package не должен экспортировать test-helpers. |
| 2.6 | `auth/refreshredis/Stats` помечена «O(N), admin/diagnostic only». Если v1 фиксит этот контракт — добавить `ErrTooManyKeys` после N (cap). | **P2** — opt-in deferred. |
| 2.7 | `auth/sessionsredis/Stats` имеет тот же O(N) — и комментарий «EXPIRED records are invisible … Expired = 0 always for this backend». До v1 — либо снять поле `Expired` из API (нелогично что всегда 0), либо задокументировать. | **P1** — асимметрия с `refreshpg.Stats` (где Expired реальный) — это API-сюрприз. |

### `clients/`

| # | Item | Приоритет |
|---|---|---|
| 2.8 | `clients/nats.Client.Conn()` и `JetStream()` — escape-hatch'и для advanced use. Если v1 — зафиксировать, что эти возвраты не покрыты `errs.Error` wrapping'ом. | **P1** — задокументировать в `clients/nats/doc.go` (или README пакета). |
| 2.9 | `clients/apimap.Engine.RegisterTransport` — mock-mode. Документировать что Mock-mode сохраняет breaker/bulkhead chain. | **P2** — добавить в `clients/apimap/README.md`. |
| 2.10 | `clients/redis.Client.Redis()` (`clients/redis/client.go:215`) возвращает `*redis.Client` — под cluster/sentinel будет `nil`. API trap: каллер может dereference. | **P0** — либо panic с понятным сообщением, либо typed error. |
| 2.11 | `clients/webhooks` — `WorkerConfig.Propagator` пока single-source-of-truth для tracing. Для v1 — задокументировать что W3C TraceContext is the only contract; не поменяется на B3 или Datadog headers. | **P1** — semver implication. |
| 2.12 | `clients/cache.Config.Codec` — pluggable, но default `JSONCodec` не экспортирован для re-use. | **P2** — детальная мелочь. |
| 2.13 | `clients/natsmap/natsgw` — почти не задокументирован, нет README в пакете. | **P1** — закрыть до v1. |

### `breaker/`, `bulkhead/`, `batch/`

| # | Item | Приоритет |
|---|---|---|
| 2.14 | `bulkhead.WithAdaptive` — `Controller` интерфейс exported, но shipped только `AIMDController`. Vegas/Gradient2 упомянуты в CHANGELOG как «later behind the same shape» — но не реализованы. Для v1 — либо реализовать (минимум один), либо явно vendor'ить `Controller` интерфейс как stable extension point. | **P1** — extension-point без реальных реализаций может выглядеть как dead code на ревью. |
| 2.15 | `breaker.Config.OnStateChange` — отлично; но `bulkhead.Config.OnCapacityChange` есть, а **`OnAcquireFail`** (для observability rejection причин) — нет. Асимметрия. | **P2** — additive после v1. |
| 2.16 | `batch.MaxRetries + RetryBackoffBase/Max` — но нет classifier (что считать retryable). Если HandlerFn возвращает `context.Canceled` — retry будет напрасно. | **P1** — добавить `Config.IsRetryable` чтоб не плодить ретраи на ctx.Done. |

### `cronmap/`

| # | Item | Приоритет |
|---|---|---|
| 2.17 | `cronmap.Runtime.TriggerJob(ctx, name)` — bypass'ит singleton lock. Документация говорит «operator override convention», но прод-сервис который случайно дёрнет это endpoint порушит leader-election invariants. | **P1** — либо переименовать в `TriggerJobAsAdmin`, либо принимать explicit `cronmap.OverrideOK{}` token. |
| 2.18 | `cronmap` schedule — 5-field (no seconds) default. Если хочется секунд — `WithParser`. Стандарт robfig/cron v3 — оба. Чётко зафиксировать default. | **P2** — в CHANGELOG задокументировано. |

### `fibermap/` extras (sse, ws, wsnats)

| # | Item | Приоритет |
|---|---|---|
| 2.19 | `fibermap/sse.Stream` — `// Not safe for concurrent use` (`fibermap/sse/sse.go:33`), но без runtime assert'а. Каллер может прострелить ногу. | **P1** — добавить `if atomic.CompareAndSwap` guard + panic-с-сообщением (как pgx'овский ConnPool). |
| 2.20 | `fibermap/wsnats.Bridge` — кит сериализует WS writes через mutex, НО не сериализует READS. Если каллер запускает goroutine на чтение — есть race на close. | **P1** — задокументировать read-lifecycle ИЛИ внутри bridge запустить read-goroutine кит-сайдом. |

### `sentrykit/`

| # | Item | Приоритет |
|---|---|---|
| 2.21 | CPU profiling deferred. Внутренняя заметка говорит «sentry-go v0.46.2 lacks stable profiling client option». Проверить актуальность на 2026-06: sentry-go вероятно подвезли. | **P1** — open issue если ещё нет, не блокер для v1. |
| 2.22 | `sentrykit.ScrubPII()` default-redaction set жёстко зашит (Authorization, Cookie, X-API-Key, …). Allow extending через `WithExtraScrubHeaders`. | **P2** — мелочь. |

### `service/`

| # | Item | Приоритет |
|---|---|---|
| 2.23 | `service.New[T, C]` — 2 type-параметра. Для каллеров не использующих auth — `C` бесполезный type-param. Можно ввести `service.NewSimple[T]` shortcut. | **P2** — quality-of-life. |
| 2.24 | `service.Service` exposes публично каждое subsystem field (`svc.DB`, `svc.Auth`, …). Если subsystem не сконфигурирован — `nil`. Каллер должен помнить про nil-check. | **P1** — `service.MustDB()` / `service.OptionalDB()` accessors сэкономят ошибки. |

---

## 3. Suggested merge order

Логика: **P0 → P1 → P2**. Закрытые items вычеркнуты выше, остаток ниже в порядке cascade'а:

1. ✓ ~~`fix/v1-bitnami-cmdkit`, `fix/v1-license-and-readme`, `docs/v1-semver-policy`, `docs/v1-security-policy`~~ — закрыты PR #152/#153.
2. ✓ ~~`chore/v1-drop-docs-packages`~~ — готов, ждёт merge (см. #1.9).
3. **`refactor/v1-db-drop-back-compat`** (P0 #2.1) — снести `HasReadReplica()`/`ReadPool()` legacy в `db.go`, **breaking change**. Лучше сделать ДО v1 чтобы не тащить deprecated в v1.
4. **`fix/v1-redis-cluster-nil-trap`** (P0 #2.10) — `clients/redis.Redis()` под cluster/sentinel.
5. *После — все P1 пунктом по теме (auth, clients, resilience…).*
6. *После всех P0 + P1 — pin `v1.0.0-rc1`, прогнать недельный bake-test в `examples/`.*
7. *Tag `v1.0.0`.*

---

## 4. Open questions для обсуждения

1. **Что означает «breaking change» в v1?** Только изменение signature exported func? Или метрика-label drop / переименование Code-константы тоже считается? *(частично закрыто `docs/versioning.md` — стоит перечитать и убедиться, что edge-cases покрыты.)*
2. **Поддерживать ли два major'а параллельно?** v1 + v2 одновременно или v1 freeze + полное переключение на v2?
3. **Минимальная Go-версия как часть semver?** Bump Go = breaking? (OTel kit считает: да; Kubernetes: нет.) *(стоит зафиксировать в `docs/versioning.md` отдельным разделом — сейчас там не упомянуто явно.)*
4. **`service/` — где провести границу?** Сейчас он почти-всё. Через год это будет монстр на 50 опций. Может быть `service/lite/` для минимального wiring и `service/full/` для всего?

---

## 5. Что НЕ в audit'е

- **Per-file code review** — gap-list собран по docs/CHANGELOG/README + точечному grep'у по hot files. Build invariants внутри функций не проверялись. Рекомендация: `/code-review ultra` на каждый затронутый пакет ДО v1.
- **Test coverage** — `go test -cover` не прогнан (нужен Docker, локально нет). CI прогон с `-coverprofile` дал бы базу: целиться в 80%+ на новые пакеты.
- **Benchmark drift** — нет benchmarks в kit. Для v1 как минимум hot-paths (`fibermap.Engine.Handle`, `db.Query` через tracer, `breaker.Allow`) — `go test -bench` baseline на CI.
- **API compat tool** — `apidiff` или `go-mod-outdated` против предыдущего тага. Когда есть v0.X tag — генерировать diff в CHANGELOG.

---

*Конец репорта. Все find'ы — это **наблюдения**, не **указания**. Решение что делать в v1 — за владельцем репо.*