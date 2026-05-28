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

## Pagination

`sqb.Page` is a stock query-param shape for list endpoints. Combined with
`sqb.QueryAll[T]` (see below), a list handler is 4–5 lines of intent:

```go
func (h *Handler) List(c *fibermap.Context[T], p sqb.Page) error {
    b := sqb.Builder.
        Select(itemColumns...).
        From("items").
        Where(sq.Eq{"user_id": c.Data.UserID}).
        OrderBy("created_at DESC")   // sort is the caller's call — allowlist columns
    items, err := sqb.QueryAll[Item](c.UserContext(), h.db, p.Apply(b), scanItem)
    if err != nil { return err }
    return c.JSON(items)
}
fibermap.RegisterHandlerWithQuery(eng, "items.list", h.List)
// → GET /items?limit=50&offset=100
```

(If you ALSO need a body / path params alongside pagination, use
`RegisterHandlerWithInput` and embed `Query sqb.Page` in the Input struct.)

| Field | Tag | Validation | Default |
|---|---|---|---|
| `Limit` | `query:"limit"` | `omitempty,min=1,max=100` | `sqb.PageDefaultLimit` (20) |
| `Offset` | `query:"offset"` | `omitempty,min=0` | 0 |

`Apply` is belt-and-suspenders: even if the engine validator is disabled, it
clamps `Limit` to `sqb.PageMaxLimit` (100) and `Offset` to ≥0.

**ORDER BY is intentionally NOT part of `Page`** — sort columns are an
SQL-injection surface. Each list endpoint should decide its own allowlist and
append `OrderBy("column DIR")` to the builder itself.

## Typed scan helpers — `QueryAll[T]` / `QueryOne[T]`

Generic helpers that fold the standard pgx scan boilerplate (`Query` →
`defer Close` → `for rows.Next()` → `rows.Scan` → `rows.Err`) into one call:

```go
// SELECT many rows.
items, err := sqb.QueryAll[Item](ctx, db,
    sqb.Builder.Select(...).From("items").Where(sq.Eq{"user_id": uid}),
    scanItem)

// SELECT / INSERT … RETURNING / UPDATE … RETURNING one row.
user, err := sqb.QueryOne[User](ctx, db,
    sqb.Builder.Insert("users").Columns("email").Values(email).
        Suffix("RETURNING id, email, created_at"),
    scanUser)
```

The scan function takes `pgx.Row` so the SAME helper works for both — and
matches the signature pgx.Rows already provides:

```go
func scanItem(r pgx.Row, dst *Item) error {
    return r.Scan(&dst.ID, &dst.Name, &dst.CreatedAt)
}
```

`QueryOne` surfaces pgx.ErrNoRows as `*errs.Error{KindNotFound}` through the
underlying `db.Querier`.

## Notes

- **`sqb.Builder` (not `sq.StatementBuilder`).** It's already wired to `sq.Dollar` placeholders. Using bare squirrel produces `?`-placeholder SQL which Postgres rejects.
- **`SqlBuilder` interface** (used by `Exec`) accepts any squirrel builder that exposes `.ToSql() (string, []any, error)` — `InsertBuilder`, `UpdateBuilder`, `DeleteBuilder`, `SelectBuilder`. `Query` is specialised to `SelectBuilder`.
- **Errors flow through `db.Querier`** — pgx errors map to `*errs.Error` via the same `mapPgxErr` as direct `db.Query`/`Exec`. No double-wrapping.
- **One-way dep:** core `db/` does NOT import `sqb`. If a service prefers raw SQL string concatenation (which is fine for static queries), skip this package entirely.
- **No ORM here.** sqb is query-building only — scanning into structs is still your call (use `db.Query` → `rows.Scan(...)`).

## See also

- [`db`](../README.md) — the underlying pool + `Querier` interface
- [Masterminds/squirrel docs](https://github.com/Masterminds/squirrel) for the full builder API
