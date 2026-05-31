# db/migrate

Zero-dependency Postgres migration runner built on the kit's
`*db.DB`. Conventions over configuration — drop SQL files into an
`embed.FS`, call `migrate.Up(ctx, d, fsys)`, and the runner picks
them up.

## Why use it

Every service has the same migration boilerplate: list files,
read them in order, exec them, remember which ones already ran.
This package is the smallest reasonable answer:

- Schema-tracking table created automatically.
- Files run in their own transaction by default (override via
  directive).
- Idempotent reruns skip already-applied files.
- No external dependencies — `service.WithMigrations(fsys)` wires
  the whole thing.

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
// migrate.Up runs after buildDB, before any subsystem that reads
// schema (auth.refreshpg, outbox).
```

Manual usage:

```go
fsys, _ := fs.Sub(migrationsFS, "migrations")
if err := migrate.Up(ctx, svc.DB, fsys); err != nil { ... }
```

## File naming convention

| Pattern | Role |
|---|---|
| `NNNN_name.sql` | Up migration. NNNN is the version key; sort lexically — use zero-padded width for portability. |
| `NNNN_name.down.sql` | Optional Down for the same NNNN. Required if you intend to run `migrate.Down`. |

`name` matches `[A-Za-z0-9._-]+`. Non-`.sql` files in the FS are
silently ignored (README.md alongside migrations doesn't trip the
parser).

## Directives

| Directive | Effect |
|---|---|
| `-- @migrate:no-transaction` on the first non-blank line | Runs the file OUTSIDE a transaction. Required for `CREATE INDEX CONCURRENTLY` and similar statements Postgres refuses to wrap. |

## API surface

| Function | Returns |
|---|---|
| `Up(ctx, d, fsys)` | Apply every pending Up in version order. Skips applied. |
| `Down(ctx, d, fsys, n)` | Roll back the n most recently applied versions. Errors with `migrate_unknown_down` if a rolled-back version has no `.down.sql`. |
| `Version(ctx, d)` | Highest applied version, "" when empty. |
| `List(ctx, d, fsys)` | Parsed Ups + Applied flag — for `kit migrate status` style tools. |
| `Parse(fsys)` | Read-only parser; returns Up slice + Down lookup. Useful for CI checks. |
| `Schema()` | (not used here — outbox-style schema embedding is per-feature.) |

## Tracking table

```sql
CREATE TABLE IF NOT EXISTS schema_migrations (
    version    text        PRIMARY KEY,
    name       text        NOT NULL,
    applied_at timestamptz NOT NULL DEFAULT NOW()
)
```

The runner bootstraps this table on every Up/Down/Version/List
call. It's idempotent so reapplying schema doesn't fail.

## Error codes

| Code | Meaning |
|---|---|
| `migrate_read_fs` | `embed.FS` read failed. |
| `migrate_invalid_filename` | `.sql` file doesn't match `NNNN_name(.down)?.sql`. |
| `migrate_duplicate_version` | Two Up files share an NNNN prefix. |
| `migrate_orphan_down` | A `.down.sql` has no matching Up. |
| `migrate_apply_failed` | A migration's SQL execution failed. |
| `migrate_rollback_failed` | A Down migration's SQL execution failed. |
| `migrate_track_failed` | INSERT/DELETE on schema_migrations failed. |
| `migrate_bootstrap_failed` | schema_migrations CREATE TABLE failed. |
| `migrate_unknown_down` | Down asked to roll back a version with no Down file. |

## Limitations

- **Postgres only.** Dialect-agnostic runners compromise the SQL-
  forward feel; the kit targets pgx + Postgres by design.
- **No cross-version rollback graph.** Down operates strictly on
  the most-recently-applied N versions.
- **No online schema changes.** pgroll / pg-osc style runners are
  out of scope.
- **Per-file Tx, not per-batch.** Up stops on first failure but
  does NOT roll back successfully-applied earlier files in the
  same call.

## See also

- [`db`](../README.md) — the underlying pool wrapper.
- [`service`](../../service/README.md) — `service.WithMigrations(fsys)` auto-wires the runner.
