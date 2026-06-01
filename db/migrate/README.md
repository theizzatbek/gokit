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
| `Up(ctx, d, fsys)` | Применяет все pending Up'ы в порядке версий. Пропускает применённые. |
| `Down(ctx, d, fsys, n)` | Откатывает n самых недавно применённых версий. Ошибка `migrate_unknown_down`, если у откатываемой версии нет `.down.sql`. |
| `Version(ctx, d)` | Самая высокая применённая версия, "" если пусто. |
| `List(ctx, d, fsys)` | Распарсенные Up'ы + флаг Applied — для тулзов вроде `kit migrate status`. |
| `Parse(fsys)` | Read-only парсер; возвращает Up-срез + Down lookup. Полезно для CI-проверок. |
| `Schema()` | (здесь не используется — outbox-style embedding схемы per-feature.) |

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
