# db/sqb

Opt-in [Masterminds/squirrel](https://github.com/Masterminds/squirrel) wrapper preconfigured for Postgres `$N` placeholders. `sqb.Builder` for query construction, `sqb.Query` and `sqb.Exec` to run a builder against any `db.Querier` (so it works against `*db.DB` AND `*db.Tx`).

**Parent:** [../README.md](../README.md)
**Import:** `github.com/theizzatbek/gokit/db/sqb`

## Use

```go
import (
    "github.com/theizzatbek/gokit/db"
    "github.com/theizzatbek/gokit/db/sqb"
)

// SELECT with dynamic conditions
b := sqb.Builder.Select("id", "email").From("users").Where(sq.Eq{"org_id": orgID})
if onlyActive {
    b = b.Where(sq.Eq{"deleted_at": nil})
}
rows, err := sqb.Query(ctx, d, b)
// … iterate with rows.Next()

// INSERT
ins := sqb.Builder.Insert("users").Columns("email", "password_hash").Values(email, hash).Suffix("RETURNING id")
tag, err := sqb.Exec(ctx, d, ins)

// Inside a transaction
err := d.Tx(ctx, func(tx *db.Tx) error {
    _, err := sqb.Exec(ctx, tx, sqb.Builder.Update("users").Set("verified_at", time.Now()).Where(sq.Eq{"id": id}))
    return err
})
```

## Notes

- **`sqb.Builder` (not `sq.StatementBuilder`).** It's already wired to `sq.Dollar` placeholders. Using bare squirrel produces `?`-placeholder SQL which Postgres rejects.
- **`SqlBuilder` interface** (used by `Exec`) accepts any squirrel builder that exposes `.ToSql() (string, []any, error)` — `InsertBuilder`, `UpdateBuilder`, `DeleteBuilder`, `SelectBuilder`. `Query` is specialised to `SelectBuilder`.
- **Errors flow through `db.Querier`** — pgx errors map to `*errs.Error` via the same `mapPgxErr` as direct `db.Query`/`Exec`. No double-wrapping.
- **One-way dep:** core `db/` does NOT import `sqb`. If a service prefers raw SQL string concatenation (which is fine for static queries), skip this package entirely.
- **No ORM here.** sqb is query-building only — scanning into structs is still your call (use `db.Query` → `rows.Scan(...)`).

## See also

- [`db`](../README.md) — the underlying pool + `Querier` interface
- [Masterminds/squirrel docs](https://github.com/Masterminds/squirrel) for the full builder API
