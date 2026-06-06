# db/testdb

Testing helpers, которые поднимают Postgres-контейнеры через `testcontainers-go` и возвращают готовые `*db.DB`. Заменяет boilerplate `TestMain + initContainer + Connect`, который сейчас дублируется в ~15 подпакетах кита.

**Импорт:** `github.com/theizzatbek/gokit/db/testdb`
**Зависит от:** `testcontainers-go`, `db/`, `errs/`
**Требования:** запущенный Docker daemon. Под `go test -short` весь пакет пропускается.

## Зачем это нужно

Каждый подпакет с Postgres-backed store у нас сейчас имеет ~30 строк одинакового кода:

```go
var pgOnce sync.Once
var pgCfg db.Config
var pgErr error

func TestMain(m *testing.M) { os.Exit(m.Run()) }

func initContainer() {
    ctx, cancel := context.WithTimeout(...)
    c, err := tcpg.Run(ctx, "postgres:16-alpine", ...)
    // … host/port lookup, build db.Config …
}

func freshDB(t *testing.T) *db.DB {
    if testing.Short() { t.Skip(...) }
    pgOnce.Do(initContainer)
    // … schema isolation …
}
```

`testdb.Spin(t)` делает то же самое в одну строку и шарит контейнер между тестами для +5-10x скорости. `testdb.SpinCluster(t, N)` дополнительно поднимает primary + N standby с streaming replication — нужен для тестирования multi-replica routing (см. [db/README.md](../README.md#multi-replica-routing)).

## Single-node: Spin

```go
import "github.com/theizzatbek/gokit/db/testdb"

func TestUserStore_RoundTrip(t *testing.T) {
    d := testdb.Spin(t)                 // skips under -short; t.Cleanup wired
    if _, err := d.Exec(ctx, `CREATE TABLE users (id int)`); err != nil {
        t.Fatal(err)
    }
    // …
}
```

### Поведение по умолчанию

- **Один контейнер на binary** — `Spin` lazy-инициализирует `postgres:16-alpine` контейнер на первом вызове, потом переиспользует его. Cleanup срабатывает после последнего теста.
- **Per-test schema isolation** — каждый вызов создаёт `test_<hex>` schema и pin'ит `search_path` к ней. `MaxConns: 1` чтобы `SET search_path` сохранялся между запросами.
- **Skip под `-short`** — все тесты с `Spin` автоматически пропускаются под `go test -short`.

### Опции

| Опция | По умолчанию | Заметки |
|---|---|---|
| `WithImage(image)` | `postgres:16-alpine` | Pinned tag для CI'я. |
| `WithDatabase(name)` | `testdb` | Имя БД внутри контейнера. |
| `WithCredentials(user, pw)` | `test`/`test` | Никогда не выходит за границы контейнера. |
| `WithStartupTimeout(d)` | 120s | Bound на cold-pull. |
| `WithMaxConns(n)` | 4 | По умолчанию `Spin` оверрайдит до 1 для search_path consistency'а; pass через `Connect` напрямую если нужно больше. |
| `WithFreshPerTest()` | off | Каждый `Spin` строит новый контейнер. Используйте только если cross-test schema isolation недостаточна. |

## Multi-node cluster: SpinCluster

```go
func TestReplicationLag_Settles(t *testing.T) {
    c := testdb.SpinCluster(t, 2)  // primary + 2 standby

    // Writes идут на Primary.
    _, err := c.Primary.Exec(ctx,
        `CREATE TABLE events (id int); INSERT INTO events VALUES (1)`)
    if err != nil { t.Fatal(err) }

    // Wait for streaming replication catch-up before reading from replicas.
    if err := c.WaitForReplication(ctx); err != nil { t.Fatal(err) }

    // c.Multi — *db.DB с Config.ReadURLs spanning all replicas.
    var n int
    if err := c.Multi.ReadQueryRow(ctx,
        `SELECT count(*) FROM events`).Scan(&n); err != nil {
        t.Fatal(err)
    }
    // n == 1
}
```

### Что возвращает Cluster

| Поле | Что |
|---|---|
| `Primary *db.DB` | Writable master. Все INSERT/UPDATE/DELETE сюда. |
| `Replicas []*db.DB` | Per-replica direct handle'ы. Используйте для probe per-replica state'а (replication lag, individual standby health). |
| `Multi *db.DB` | Один *db.DB с `Config.ReadURLs` спанящий все replicas. ReadQuery/ReadQueryRow ротируются через kit's normal routing — соответствует production-выкату. |

### Как работает

- **Image: `bitnamilegacy/postgresql:16`** — Bitnami's image имеет env-driven streaming-replication wiring out of the box (`POSTGRESQL_REPLICATION_MODE=master|slave`). Vanilla `postgres:` image потребовал бы pg_basebackup + recovery.conf скрипты внутри testcontainer setup'а.
- **Docker network** — primary и все standby живут в общей сети; standbys резолвят primary через alias `"primary"`.
- **Per-call контейнеры по умолчанию** — `SpinCluster` ВСЕГДА строит новые контейнеры на вызов, потому что cross-test state в реплицирующей паре сложно почистить автоматически (streaming WAL, `pg_stat_replication`, `pg_hba.conf` тоже не пере-`SET search_path`-ишь). См. `BootCluster` ниже для package-level reuse.
- **WaitForReplication** — блокирует пока `pg_current_wal_lsn() - pg_last_wal_replay_lsn() == 0` на каждом replica или ctx не expired'нулся. Polling каждые 50ms.

### Опции (в дополнение к Spin'овым)

| Опция | По умолчанию | Заметки |
|---|---|---|
| `WithClusterImage(image)` | `bitnamilegacy/postgresql:16` | Должен поддерживать `POSTGRESQL_REPLICATION_MODE`. |
| `WithReplicationCredentials(u, pw)` | `repl`/`repl` | Replication-account для streaming. |
| `WithConfigKVs(map)` | none | Extra `postgresql.conf` ключи на primary (passed как `-c key=value`). Полезно для тестов, которым нужно специфическое `wal_level` / `max_wal_senders` / etc. |

### Performance-заметки

- Cold pull `bitnamilegacy/postgresql:16` — ~600MB; первый запуск может занять минуты. Затем cached.
- Warm boot 1 primary + 1 replica — ~15s локально, ~30s в CI с холодным Docker socket'ом.
- Boot scales примерно линейно с количеством replicas. Не запускайте `SpinCluster(t, 10)` в каждом тесте.

## Best practices

- **Используйте `Spin` (а не `SpinCluster`) везде где возможно** — он в 10-50x быстрее.
- **Per-test schema через `Spin` обычно достаточно** для cross-test isolation'а. Reach for `WithFreshPerTest` только когда confirmed cross-test interaction.
- **Cluster boot занимает время** — для повторных cluster-зависимых тестов внутри одного пакета используйте `BootCluster` из `TestMain` (см. ниже), boot платится один раз.
- **Не забывайте `WaitForReplication`** перед каждым read'ом с replicas — без этого получите flaky tests когда replica lag > 0.

### Package-level reuse через `BootCluster`

`SpinCluster` нужен `*testing.T` и регистрирует cleanup через `t.Cleanup` — отлично для per-test изоляции, но 15-30s boot платится за каждый cluster-зависимый тест. Если у вас несколько таких тестов в одном пакете, шарьте кластер через `TestMain`:

```go
var shared *testdb.Cluster

func TestMain(m *testing.M) {
    if testing.Short() {
        os.Exit(m.Run())
    }
    ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
    defer cancel()
    cluster, teardown, err := testdb.BootCluster(ctx, 1)
    if err != nil {
        log.Fatalf("BootCluster: %v", err)
    }
    defer teardown()
    shared = cluster
    os.Exit(m.Run())
}

func TestSomething(t *testing.T) {
    // используем shared.Primary / shared.Replicas / shared.Multi
}
```

**Trade-off.** `SpinCluster` даёт каждому тесту fresh cluster (WAL, replication state, `pg_stat_replication` — всё чистое). `BootCluster` шарит один cluster — caller сам отвечает за cross-test isolation: TRUNCATE rows между тестами, наблюдает за WAL/replication state, который может протечь, пере-создаёт schemas если test делает DDL. Kit ничего не enforce'ит — helper намеренно raw.

`teardown` non-nil даже при `err != nil` — partial boot оставляет containers + network, caller должен дёрнуть `teardown` чтобы освободить. Повторный вызов `teardown` безопасен, но бесполезен.

## Limitations

- **Bitnami image тяжелый** — ~600MB vs 80MB для postgres-alpine. Compromise: out-of-the-box replication.
- **Net-mode только bridge** — kit не пробует host-mode network'ов; работает на macOS/Linux/Windows одинаково.
- **WaitForReplication опрашивает каждые 50ms** — для тестов с очень высоким write-throughput'ом это может быть hot loop. Оверрайдьте через свой собственный polling если нужно.

## См. также

- [`db/`](../README.md) — родительский пакет: `db.Config{ReadURLs, ReadStrategy}` + `db.ReadFromPrimary(ctx)` + `db.ReplicationLag(ctx)`.
- [testcontainers-go/modules/postgres](https://golang.testcontainers.org/modules/postgres/) — upstream module.
- [Bitnami PostgreSQL Image (legacy mirror)](https://hub.docker.com/r/bitnamilegacy/postgresql) — replication env vars reference.
