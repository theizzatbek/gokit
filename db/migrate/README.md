# db/migrate

Zero-dependency Postgres migration runner на базе китового `*db.DB`.
Конвенции вместо конфигурации — кидайте SQL-файлы в `embed.FS`,
вызывайте `migrate.Up(ctx, d, fsys)`, и runner их подхватит.

## Зачем это нужно

У каждого сервиса один и тот же migration boilerplate: перечислить
файлы, прочитать их в порядке, выполнить, помнить какие уже
запускались. Этот пакет — самый разумный минимальный ответ:

- Schema-tracking таблица создаётся автоматически.
- Файлы по умолчанию запускаются в своих транзакциях (override через директиву).
- Идемпотентные перезапуски пропускают уже применённые файлы.
- Никаких внешних зависимостей — `service.WithMigrations(fsys)`
  подключает всё.

## Quickstart

```go
import (
    "embed"
    "github.com/theizzatbek/gokit/db/migrate"
    "github.com/theizzatbek/gokit/service"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

svc, _ := service.New(ctx, cfg,
    service.WithMigrations(migrationsFS))
// migrate.Up запускается после buildDB, до любой подсистемы,
// читающей схему (auth.refreshpg, outbox).
```

Ручное использование:

```go
fsys, _ := fs.Sub(migrationsFS, "migrations")
if err := migrate.Up(ctx, svc.DB, fsys); err != nil { ... }
```

## Конвенция именования файлов

| Шаблон | Роль |
|---|---|
| `NNNN_name.sql` | Up-миграция. NNNN — version key; сортируется лексически — используйте zero-padded ширину для portability. |
| `NNNN_name.down.sql` | Опциональный Down для того же NNNN. Обязателен, если вы планируете запускать `migrate.Down`. |

`name` матчит `[A-Za-z0-9._-]+`. Не-`.sql` файлы в FS молча
игнорируются (README.md рядом с миграциями не валит парсер).

## Директивы

| Директива | Эффект |
|---|---|
| `-- @migrate:no-transaction` на первой непустой строке | Запускает файл ВНЕ транзакции. Нужно для `CREATE INDEX CONCURRENTLY` и подобных statements, которые Postgres отказывается оборачивать. |

## API-поверхность

| Функция | Возвращает |
|---|---|
| `Up(ctx, d, fsys, opts...)` | Применяет все pending Up'ы в порядке версий. Пропускает применённые. Опции: `WithLock(name)`. |
| `UpTo(ctx, d, fsys, target, opts...)` | Применяет до версии target (включительно). Опции: те же. |
| `Down(ctx, d, fsys, n)` | Откатывает n самых недавно применённых версий. Ошибка `migrate_unknown_down`, если у откатываемой версии нет `.down.sql`. |
| `DownTo(ctx, d, fsys, target)` | Откатывает до target (не включая сам target). |
| `Plan(ctx, d, fsys)` / `Pending(ctx, d, fsys)` | Pending Up'ы. `Pending` — read-friendly alias. |
| `DryRun(ctx, d, fsys, w)` | Печатает человекочитаемый план pending миграций в w. Не выполняет SQL. |
| `Version(ctx, d)` | Самая высокая применённая версия, "" если пусто. |
| `History(ctx, d)` | `[]AppliedRecord{Version, Name, AppliedAt}` desc — drives `/admin/migrations`. |
| `List(ctx, d, fsys)` | Распарсенные Up'ы + флаг Applied — для тулзов вроде `kit migrate status`. |
| `Parse(fsys)` | Read-only парсер; возвращает Up-срез + Down lookup. Полезно для CI-проверок. |
| `Generate(dir, name, opts...)` | Скаффолдит `NNNN_name.sql` (next-NNNN или timestamp). Опции: `WithDown()`, `WithTimestamp()`. |

## Advisory-lock guard

Multi-replica boot (k8s rollout, HPA scale-up) запускает `migrate.Up` параллельно — если ничего не делать, реплики гонятся за одной и той же миграцией, и одна из них падает на `duplicate key`. `WithLock(name)` оборачивает apply-цикл в `pg_advisory_lock`: один-единственный winner применяет, остальные блокируются, и после release видят миграции уже-применёнными и no-op'ят.

```go
err := migrate.Up(ctx, svc.DB, migrationsFS,
    migrate.WithLock("myservice.migrations"))
```

Имя лока должно быть одно через все реплики одного сервиса; ключ деривируется через `sha256(name)`. Session-level лок — если процесс упал mid-apply, conn возвращается в пул и lock auto-release'ится. Пустая строка → no-lock (back-compat).

## DryRun + Pending

```go
// Pending — какие миграции применит следующий Up.
pending, _ := migrate.Pending(ctx, svc.DB, migrationsFS)
if len(pending) == 0 { /* nothing to do */ }

// DryRun — печатает план в Writer без выполнения SQL.
var buf bytes.Buffer
n, _ := migrate.DryRun(ctx, svc.DB, migrationsFS, &buf)
fmt.Print(buf.String())
// # 2 pending migrations
//
// ── 0042_add_users_email_index.sql ──────────────
// CREATE INDEX users_email_idx ON users(email);
//
// ── 0043_add_orders_state.sql ──────────────
// ALTER TABLE orders ADD COLUMN state text NOT NULL DEFAULT 'new';
```

Use как pre-flight gate в CI ("`--dry-run` перед deploy'ем; fail если diff не совпадает с expected") или как тело `kit migrate plan`-subcommand'а.

## History для /admin

```go
recs, _ := migrate.History(ctx, svc.DB)
// []AppliedRecord{Version, Name, AppliedAt}  — newest first.
```

Прямой select из `schema_migrations` без fsys-разрешения — драйв для `/admin/migrations` endpoint'а.

## Generate scaffold

```go
// Next-NNNN (читает dir, выбирает highest + 1, zero-pad to 4).
path, _ := migrate.Generate("./db/migrations", "add_users_email_index")
// → ./db/migrations/0042_add_users_email_index.sql

// С Down-stub'ом.
path, _ := migrate.Generate("./db/migrations", "rename_field",
    migrate.WithDown())
// → 0043_rename_field.sql + 0043_rename_field.down.sql

// Timestamp-stamped (для shop'ов, где multiple devs land migrations
// independently — sequential NNNN scheme provokes merge conflicts).
path, _ := migrate.Generate("./db/migrations", "audit",
    migrate.WithTimestamp())
// → 20260604093015_audit.sql
```

Up-файл стампится с `-- migrate: up <name>` line stub'ом; Down — пустой. `name` валидируется по `[A-Za-z0-9._-]+` (та же alphabet что в parser'е). Refuse-to-clobber — `O_EXCL` write, не silent overwrite.

## Tracking-таблица

```sql
CREATE TABLE IF NOT EXISTS schema_migrations (
    version    text        PRIMARY KEY,
    name       text        NOT NULL,
    applied_at timestamptz NOT NULL DEFAULT NOW()
)
```

Runner bootstrap'ит эту таблицу на каждый Up/Down/Version/List
вызов. Идемпотентно, так что повторное применение схемы не падает.

## Error codes

| Code | Смысл |
|---|---|
| `migrate_read_fs` | Чтение `embed.FS` зафейлилось. |
| `migrate_invalid_filename` | `.sql` файл не соответствует `NNNN_name(.down)?.sql`. |
| `migrate_duplicate_version` | Два Up-файла делят NNNN-префикс. |
| `migrate_orphan_down` | У `.down.sql` нет совпадающего Up. |
| `migrate_apply_failed` | Выполнение SQL миграции зафейлилось. |
| `migrate_rollback_failed` | Выполнение SQL Down-миграции зафейлилось. |
| `migrate_track_failed` | INSERT/DELETE на schema_migrations зафейлился. |
| `migrate_bootstrap_failed` | CREATE TABLE schema_migrations зафейлился. |
| `migrate_unknown_down` | Down просили откатить версию без Down-файла. |
| `migrate_lock_failed` | `WithLock`: Acquire на migration-lock errored. |
| `migrate_generate_invalid_name` | `Generate`: name пустой / с unsafe chars. |
| `migrate_generate_failed` | `Generate`: mkdir / write / permission errored. |

## Ограничения

- **Только Postgres.** Dialect-agnostic runner'ы компрометируют
  SQL-forward ощущение; кит таргетит pgx + Postgres by design.
- **Нет cross-version rollback графа.** Down работает строго с N
  самыми недавно применёнными версиями.
- **Нет online schema changes.** Runner'ы стиля pgroll / pg-osc
  вне scope'а.
- **Per-file Tx, не per-batch.** Up останавливается на первом
  failure, но НЕ откатывает успешно применённые ранее файлы в том
  же вызове.

## См. также

- [`db`](../README.md) — обёртка пула под капотом.
- [`service`](../../service/README.md) — `service.WithMigrations(fsys)` авто-подключает runner.
</content>
