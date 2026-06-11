# v1 Readiness Audit

> **Historical.** Tagged `v1.0.0-rc1` and promoted to `v1.0.0` on
> 2026-06-11 (no bake-week delay; race-regression coverage falls back
> to the nightly `race.yml` workflow). This file is now a frozen record
> of the pre-v1 audit-close work; further v1-prep tracking lives in
> CHANGELOG + git log.

Снимок: 2026-06-10, post-merge sweep перед `v1.0.0-rc1`. Исходный аудит
(2026-06-06 на `main @ 29e1838`) флагировал двенадцать P0/P1 items и
бак-овых P2-мелочей; с тех пор все P0 и P1 закрыты отдельными
feature-branch'ами и смерджены в main, а P2 консолидирован в
`feat/v1-p2-bucket` одной волной. Этот файл — снимок «что закрыто чем»
для аудита и для финального rc1-tag'а.

---

## Закрыто

### Repo-wide bucket A (PR #152 + PR #153 + `chore/v1-drop-docs-packages`)

- ✓ #1.1 semver-policy → [`docs/versioning.md`](versioning.md) (PR #153)
- ✓ #1.2 LICENSE copyright → `gokit contributors` (PR #152)
- ✓ #1.3 [`SECURITY.md`](../SECURITY.md) (PR #153)
- ✓ #1.4 `cmd/kit/gen_db_cluster.go` → `bitnamilegacy/postgresql:16` (PR #152)
- ✓ #1.5 Go-версия README ↔ go.mod синхронизированы на `1.26.4` (PR #152)
- ✓ #1.6 go.mod bump до `1.26.4` (PR #152)
- ✓ #1.7 [`CONTRIBUTING.md`](../CONTRIBUTING.md) (PR #152)
- ✓ #1.8 Decision Guide вынесен в [`docs/decision-guide.md`](decision-guide.md) (PR #152)
- ✓ #1.9 → снят с повестки: `docs/packages/` дропнут в `chore/v1-drop-docs-packages`; canonical-источник API-контрактов — `<subpkg>/README.md` + `doc.go` рядом с кодом

### P0 — закрыты

- ✓ #2.1 `db.(*DB).HasReadReplica()` + `(*DB).ReadPool()` снесены — single-pool back-compat shims удалены; новый API: `(*DB).ReadPools()` для полного набора (с именами, healthy и lag) или `len(ReadPools()) > 0` для boolean'а. `Config.HasReadReplica` (env-knob) остаётся. → `refactor/v1-db-drop-back-compat` (`47341d8`).
- ✓ #2.10 `clients/redis.(*Client).Redis()` под cluster/sentinel — fail-fast panic с guiding-сообщением вместо silent nil-dereference; `clients/ratelimit.NewRedis` возвращает `*errs.Error{Code: CodeInvalidConfig}` вместо паники через `rc.Redis()`. → `fix/v1-redis-cluster-nil-trap` (`62b0da9`) + sentinel-detection follow-up `fix/v1-redis-sentinel-panic-detect` (`b6d29dd` — type-assertion не отличает FailoverClient от обычного `*redis.Client`, mode-field теперь source of truth).

### P1 — закрыты

- ✓ #1.10 `-race` в integration job — **сознательно отложено** ([`.github/workflows/test.yml:127-130`](../.github/workflows/test.yml)). Race + testcontainers тройной wall time без новых багов на практике; race-prone core покрыт `unit-race` job'ом. Если потом захочется matrix-job вокруг testdb — additive, не блокер.
- ✓ #2.2 `db/testdb` cluster-reuse — `BootCluster` для non-`*testing.T` bootstrap'а в `TestMain` (`cf29755`).
- ✓ #2.5 `auth.SetPrincipalForTest[C]` → `auth/authtest.SetPrincipal[C]`; Locals-key унесён в `auth/internal/principalkey` чтобы sibling-пакет писал в тот же слот, не экспортируя ключ наружу (`4c31c6f`).
- ✓ #2.7 `auth/sessions.StoreStats.Expired` снесён из публичного типа; кросс-backend контракт теперь `{Active, Total}` (`ed88bda`).
- ✓ #2.8 `clients/nats.Client.Conn()` + `JetStream()` escape-hatch контракт прописан в `clients/nats/doc.go` — возвраты не покрыты `errs.Error` wrapping'ом (`37e7b57`).
- ✓ #2.11 `clients/webhooks.WorkerConfig.Propagator` — W3C TraceContext зафиксирован как единственный tracing-injection контракт; не сменится на B3/Datadog headers (`d9139e9`).
- ✓ #2.13 `clients/natsmap/natsgw` README — Observability + when-not-to-use разделы + cross-link из natsmap (`3312833`).
- ✓ #2.14 `bulkhead.VegasController` — TCP-Vegas-style adaptive capacity-law под общим `Controller` интерфейсом (extension-point теперь не dead-code) (`adab809`).
- ✓ #2.16 `batch.Config.IsRetryable` classifier + fix retry-leak на `ctx.Done()` (`c25a0b5`).
- ✓ #2.17 `cronmap.Runtime.TriggerJob` теперь требует explicit `OverrideOK{}` token — случайный hit endpoint'а больше не порушит leader-election invariants (`bd8d8ea`).
- ✓ #2.19 `fibermap/sse.Stream` — CAS-guard против concurrent `Send/SendJSON/Comment`; нарушение конкурентного контракта = panic с guiding-сообщением (`6585e22`).
- ✓ #2.20 `fibermap/wsnats.Bridge` — read unblock'ается на ctx-cancel; concurrency-контракт (read-lifecycle owned by caller) задокументирован (`8903c36`).
- ✓ #2.21 sentrykit CPU profiling — re-confirmed deferral для v1-freeze; sentry-go всё ещё без stable profiling-client option на 2026-06 (`a05328b`).
- ✓ #2.24 `service.Service` — typed `MustX`/`OptionalX` accessors для каждой optional-subsystem; nil-check больше не на каллере (`0bfb4cb`).

### P2 — закрыты в `feat/v1-p2-bucket` (`6c0ecc3`)

Все pre-v1 P2-мелочи собраны одной волной, чтобы не плодить per-item PR'ы.

Additive code:
- ✓ #2.6 `auth/refreshredis.WithStatsCap(n)` + sentinel `ErrStatsCapExceeded` — opt-in cap для O(N) Stats; caller branchится через `errors.Is` и выбирает между «расширить cap» и «re-scope через `ListBySubject`».
- ✓ #2.15 `bulkhead.Config.OnAcquireFail func(reason string)` — симметрично `OnCapacityChange`; panic-safe; те же reason-labels что и у Prometheus-коллектора.
- ✓ #2.22 `sentrykit.WithExtraScrubHeaders(headers ...string)` — расширяет default ScrubPII set; threaded через `ScrubPII()` и `WithoutPII()` как variadic `ScrubOption`.
- ✓ #2.23 `service.NewSimple(ctx, cfg, opts...)` — zero-type-param shortcut для `New[struct{}, struct{}]`.

Repo housekeeping:
- ✓ #1.11 `CODE_OF_CONDUCT.md` (Contributor Covenant 2.1).
- ✓ #1.12 `.github/ISSUE_TEMPLATE/{bug_report,feature_request,config}.yml`.
- ✓ #1.13 CHANGELOG split — все released `[v0.x.y]` секции в `docs/CHANGELOG-0.x.md`; основной `CHANGELOG.md` оставляет `[Unreleased]` + secции с последнего релиза (1382 → ~820 строк; архив ~570).

Doc-only verifications (стейл-аудит, кода не требовалось):
- ✓ #2.3 `db/sqb/` — squirrel pinned to 1.5.4 (latest); monitoring only.
- ✓ #2.4 `db/migrate.Generate` — `WithTimestamp()` vs default next-NNNN modes уже задокументировано в README.
- ✓ #2.9 `clients/apimap.Engine.RegisterTransport` mock-mode — README уже описывает что breaker/bulkhead chain сохраняется.
- ✓ #2.12 `clients/cache.JSONCodec` — уже exported как default codec; audit-note флагировал «not exported» по стейл-данным.
- ✓ #2.18 `cronmap` 5-field default + `WithParser` для seconds-precision — уже зафиксировано в README и CHANGELOG.

---

## Что осталось до `v1.0.0-rc1`

1. **Push + merge `feat/v1-p2-bucket`** — последний open bucket; после этого main = canonical pre-rc1 surface.
2. **Подчистить локальные ветки** — `refactor/v1-*`, `feat/v1-*`, `fix/v1-*`, `docs/v1-*` все смерджены (`ahead=0` к main); можно `git branch -d` локально и `git push origin --delete` на remote, чтобы branch-листинг не шумел.
3. **Tag `v1.0.0-rc1`** + минимум недельный bake-test в `examples/`.
4. **Tag `v1.0.0`** — после bake'а.

---

## Open questions для `v1.0.0` owner-call

Не блокеры для rc1, но имеет смысл закрыть до final tag'а — каждый вопрос меняет смысл semver-обещаний.

1. **Что считать «breaking» в v1?** Только signature exported func? Или метрика-label drop / переименование Code-константы тоже? *(частично закрыто `docs/versioning.md` — стоит перечитать и убедиться, что edge-cases покрыты.)*
2. **Поддерживать ли v1 + v2 параллельно?** Или v1-freeze + полное переключение на v2 после релиза второго мажора?
3. **Минимальная Go-версия как часть semver?** Bump Go = breaking? (OTel kit: да; Kubernetes: нет.) *(сейчас не упомянуто в `docs/versioning.md` явно — стоит зафиксировать отдельным разделом.)*
4. **`service/` — границы.** Сейчас он почти-всё. Через год это будет монстр на 50 опций. Может `service/lite/` для минимального wiring и `service/full/` для всего?

---

## Что НЕ в audit'е (рекомендации к rc1 bake)

- **Per-file code review** — gap-list собран по docs/CHANGELOG/README + точечному grep'у по hot files. Build invariants внутри функций не проверялись. Рекомендация: `/code-review ultra` на каждый затронутый пакет ДО `v1.0.0` tag'а.
- **Test coverage** — `go test -cover` не прогнан (нужен Docker, локально нет). CI прогон с `-coverprofile` дал бы базу: целиться в 80%+ на новые пакеты.
- **Benchmark drift** — нет benchmarks в kit. Для v1 как минимум hot-paths (`fibermap.Engine.Handle`, `db.Query` через tracer, `breaker.Allow`) — `go test -bench` baseline на CI.
- **API compat tool** — `apidiff` или `go-mod-outdated` против предыдущего тага. Когда есть `v0.X` tag — генерировать diff в CHANGELOG.

---

*Конец репорта. Снимок отражает state на 2026-06-10. Следующий пас (если понадобится) — уже на rc1 bake-фазе.*