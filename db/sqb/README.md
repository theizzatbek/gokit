# db/sqb

Опциональная обёртка над [Masterminds/squirrel](https://github.com/Masterminds/squirrel), преднастроенная под Postgres `$N` placeholders. `sqb.Builder` для построения запросов, `sqb.Query` и `sqb.Exec` для запуска builder'а против любого `db.Querier` (так что работает и с `*db.DB`, и с `*db.Tx`).

**Родитель:** [../README.md](../README.md)
**Импорт:** `github.com/theizzatbek/gokit/db/sqb`

## Использование

```go
import (
    "github.com/theizzatbek/gokit/db"
    "github.com/theizzatbek/gokit/db/sqb"
)

// SELECT с динамическими условиями
b := sqb.Builder.Select("id", "email").From("users").Where(sq.Eq{"org_id": orgID})
if onlyActive {
    b = b.Where(sq.Eq{"deleted_at": nil})
}
rows, err := sqb.Query(ctx, d, b)
// … итерируем rows.Next()

// INSERT
ins := sqb.Builder.Insert("users").Columns("email", "password_hash").Values(email, hash).Suffix("RETURNING id")
tag, err := sqb.Exec(ctx, d, ins)

// Внутри транзакции
err := d.Tx(ctx, func(tx *db.Tx) error {
    _, err := sqb.Exec(ctx, tx, sqb.Builder.Update("users").Set("verified_at", time.Now()).Where(sq.Eq{"id": id}))
    return err
})
```

## Пагинация

`sqb.Page` — это стандартная форма query-параметров для list-эндпоинтов. В сочетании с `sqb.QueryAll[T]` (см. ниже) list-хендлер сводится к 4-5 строкам интента:

```go
func (h *Handler) List(c *fibermap.Context[T], p sqb.Page) error {
    b := sqb.Builder.
        Select(itemColumns...).
        From("items").
        Where(sq.Eq{"user_id": c.Data.UserID}).
        OrderBy("created_at DESC")   // sort решает caller — allowlist колонок
    items, err := sqb.QueryAll[Item](c.UserContext(), h.db, p.Apply(b), scanItem)
    if err != nil { return err }
    return c.JSON(items)
}
fibermap.RegisterHandlerWithQuery(eng, "items.list", h.List)
// → GET /items?limit=50&offset=100
```

(Если ещё нужен body / path-параметры рядом с пагинацией, используйте `RegisterHandlerWithInput` и заэмбедьте `Query sqb.Page` в Input struct.)

| Поле | Тэг | Валидация | По умолчанию |
|---|---|---|---|
| `Limit` | `query:"limit"` | `omitempty,min=1,max=100` | `sqb.PageDefaultLimit` (20) |
| `Offset` | `query:"offset"` | `omitempty,min=0` | 0 |

`Apply` — belt-and-suspenders: даже если валидатор engine отключён, она клампит `Limit` до `sqb.PageMaxLimit` (100) и `Offset` до ≥0.

**ORDER BY намеренно НЕ часть `Page`** — sort-колонки — это SQL-injection surface. Каждый list-эндпоинт должен решать свой собственный allowlist и добавлять `OrderBy("column DIR")` в builder сам.

### `Sort` с allowlist — безопасный пользовательский sort

`sqb.Sort` снимает ручную обвязку sort-allowlist с каждого хендлера. Парсит comma-separated строку (с `-` префиксом для DESC), валидирует против переданного safelist'а и приклеивает соответствующие `ORDER BY` клозы:

```go
type ListInput struct {
    Query struct {
        sqb.Page
        Sort string `query:"sort"`
    }
}

func (h *Handler) List(c *fibermap.Context[T], in ListInput) error {
    b := sqb.Builder.Select(itemColumns...).From("items").
        Where(sq.Eq{"user_id": c.Data.UserID})

    b, err := sqb.Sort(b, in.Query.Sort, map[string]string{
        "name":       "items.name",
        "created_at": "items.created_at",
    })
    if err != nil { return err } // *errs.Error{KindValidation} → 400

    items, err := sqb.QueryAll[Item](c.UserContext(),
        h.db, in.Query.Page.Apply(b), scanItem)
    // ...
}
// → GET /items?sort=-created_at,name → ORDER BY items.created_at DESC, items.name ASC
```

`ParseSort` и `ApplySort` — раздельные шаги, если нужно вмешаться между ними (например, проверить что не-разрешённый default-sort точно отсутствует).

Неизвестное поле сурфейсится как `*errs.Error{Code: sqb_invalid_sort}` (400 через fibermap).

## InBatches — chunked iteration

`sqb.InBatches` разбивает большой slice на чанки фиксированного размера и вызывает fn на каждом. Удобно для bulk операций где `WHERE id IN (...)` упёрся бы в лимит pgx-параметров (Postgres bind cap ≈ 65535), или чтобы ограничить время удержания row-locks при массовом UPDATE:

```go
err := sqb.InBatches(ids, 1000, func(chunk []uuid.UUID) error {
    _, err := sqb.Exec(ctx, db,
        sqb.Builder.Delete("items").Where(sq.Eq{"id": chunk}))
    return err
})
```

Generic'ный, останавливается на первой ошибке. `size <= 0` — panic (programmer error).

## CursorPage — keyset-пагинация

Альтернатива offset-пагинации для feed'ов где новые записи постоянно вставляются (offset drift'ит — page 2 повторяет элементы из page 1). Cursor — opaque base64-кодированная пара `(created_at, id)`:

```go
type ListInput struct {
    Query sqb.CursorPage
}

func (h *Handler) List(c *fibermap.Context[T], in ListInput) error {
    b := sqb.Builder.Select(itemColumns...).From("items").
        OrderBy("created_at DESC", "id DESC")

    b, err := in.Query.Apply(b, "items.created_at", "items.id")
    if err != nil { return err } // bad cursor → 400

    items, err := sqb.QueryAll[Item](c.UserContext(), h.db, b, scanItem)
    if err != nil { return err }

    var next string
    if n := len(items); n > 0 {
        last := items[n-1]
        next = sqb.Cursor{CreatedAt: last.CreatedAt, ID: last.ID}.Encode()
    }
    return c.JSON(fiber.Map{"items": items, "next": next})
}
// → GET /items?limit=20&after=<cursor>
```

`createdCol` / `idCol` хардкодятся в хендлере — это SQL-spliced, никогда от user input. Таблица должна быть упорядочена `(created_at DESC, id DESC)` для семантики "newest first". Bad cursor → `*errs.Error{Code: sqb_invalid_cursor}` (400).

## Типизированные scan-хелперы — `QueryAll[T]` / `QueryOne[T]`

Generic-хелперы, которые сворачивают стандартный pgx scan boilerplate (`Query` → `defer Close` → `for rows.Next()` → `rows.Scan` → `rows.Err`) в один вызов:

```go
// SELECT много строк.
items, err := sqb.QueryAll[Item](ctx, db,
    sqb.Builder.Select(...).From("items").Where(sq.Eq{"user_id": uid}),
    scanItem)

// SELECT / INSERT … RETURNING / UPDATE … RETURNING одна строка.
user, err := sqb.QueryOne[User](ctx, db,
    sqb.Builder.Insert("users").Columns("email").Values(email).
        Suffix("RETURNING id, email, created_at"),
    scanUser)
```

scan-функция принимает `pgx.Row`, так что ОДИН хелпер работает для обоих — и соответствует подписи, которую pgx.Rows уже предоставляет:

```go
func scanItem(r pgx.Row, dst *Item) error {
    return r.Scan(&dst.ID, &dst.Name, &dst.CreatedAt)
}
```

`QueryOne` поднимает pgx.ErrNoRows как `*errs.Error{KindNotFound}` через лежащий снизу `db.Querier`.

## Заметки

- **`sqb.Builder` (не `sq.StatementBuilder`).** Он уже подключён к `sq.Dollar` placeholders. Использование чистого squirrel производит SQL с `?`-placeholder'ами, которые Postgres отклоняет.
- **Интерфейс `SqlBuilder`** (используемый `Exec`) принимает любой squirrel-builder, у которого есть `.ToSql() (string, []any, error)` — `InsertBuilder`, `UpdateBuilder`, `DeleteBuilder`, `SelectBuilder`. `Query` специализирован на `SelectBuilder`.
- **Ошибки проходят через `db.Querier`** — pgx-ошибки маппятся в `*errs.Error` через тот же `mapPgxErr`, что и прямой `db.Query`/`Exec`. Без двойного оборачивания.
- **One-way зависимость:** core `db/` НЕ импортирует `sqb`. Если сервис предпочитает сырое склеивание SQL-строк (что нормально для статических запросов), пропустите этот пакет целиком.
- **Никакой ORM здесь.** sqb — это только query-building; scanning в struct'ы — всё ещё ваше дело (используйте `db.Query` → `rows.Scan(...)`).

## См. также

- [`db`](../README.md) — лежащий снизу pool + интерфейс `Querier`
- [Masterminds/squirrel docs](https://github.com/Masterminds/squirrel) для полного builder API
</content>
