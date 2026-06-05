package db

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/theizzatbek/gokit/errs"
)

// Page is the result of one keyset-pagination round-trip via
// [Paginate]. Items carries the scanned rows; NextCursor is an
// opaque string the caller passes back to fetch the following page,
// or empty when no further rows are available.
//
// Opaque cursor format: base64(json([cursor_key]))
// — the cursor_key is whatever scan func returned together with each
// row. Callers MUST treat NextCursor as opaque; the kit is free to
// change the encoding between minor versions.
type Page[T any] struct {
	Items      []T
	NextCursor string
}

// ScanWithCursor is the per-row scan func [Paginate] expects. Returns
// the assembled T value plus the cursor_key — typically the row's
// primary key OR (timestamp, id) tuple for tie-breakers — used to
// build the NextCursor field on the returned Page.
//
// The returned cursor_key is consumed by Paginate ONLY when the row
// is the last in the page; otherwise it is discarded.
type ScanWithCursor[T any, K any] func(rows pgx.Rows) (T, K, error)

// Paginate runs `sql` and returns one keyset-paginated [Page]. The
// caller is responsible for the SQL itself — Paginate is intentionally
// SQL-agnostic, only the cursor encoding + slice assembly is shared.
//
// Typical SQL shape:
//
//	SELECT id, email, created_at FROM users
//	WHERE ($1::text = '' OR id > $1)   -- cursor; first page passes ''
//	ORDER BY id
//	LIMIT $2 + 1                       -- +1 so we can detect "has next"
//
// The +1 trick is what makes the helper work: if the result count
// equals limit+1, the last row is dropped from Items and its cursor
// becomes NextCursor; otherwise NextCursor stays empty.
//
// Cursor encoding: the caller passes the previous NextCursor as one
// of the SQL args. To READ the cursor in Go (for SQL building), use
// [DecodeCursor]; the kit provides a generic decoder.
func Paginate[T any, K any](
	ctx context.Context,
	q Querier,
	sql string,
	limit int,
	scan ScanWithCursor[T, K],
	args ...any,
) (Page[T], error) {
	if q == nil {
		return Page[T]{}, errs.Validation("db_nil_querier", "db.Paginate: querier is nil")
	}
	if limit <= 0 {
		return Page[T]{}, errs.Validation("db_invalid_limit", "db.Paginate: limit must be > 0")
	}
	if scan == nil {
		return Page[T]{}, errs.Validation("db_nil_scan", "db.Paginate: scan func is nil")
	}
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return Page[T]{}, err
	}
	defer rows.Close()

	items := make([]T, 0, limit+1)
	cursors := make([]K, 0, limit+1)
	for rows.Next() {
		v, k, err := scan(rows)
		if err != nil {
			return Page[T]{}, mapPgxErr(err)
		}
		items = append(items, v)
		cursors = append(cursors, k)
	}
	if err := rows.Err(); err != nil {
		return Page[T]{}, mapPgxErr(err)
	}

	out := Page[T]{Items: items}
	if len(items) > limit {
		// The +1 sentinel row signals there's another page. Drop the
		// sentinel from Items; emit the cursor for the LAST row we
		// actually returned (items[limit-1]). The next page query is
		// `WHERE key > NextCursor` which yields the dropped sentinel
		// as its first row — exactly the page boundary we want.
		out.Items = items[:limit]
		cursor, err := EncodeCursor(cursors[limit-1])
		if err != nil {
			return Page[T]{}, errs.Wrap(err, errs.KindInternal, "db_cursor_encode_failed",
				"db.Paginate: cursor encode")
		}
		out.NextCursor = cursor
	}
	return out, nil
}

// EncodeCursor renders an arbitrary value as the opaque base64-JSON
// cursor [Paginate] returns in [Page.NextCursor]. The kit guarantees
// round-trip safety across [DecodeCursor]; the on-wire format is
// otherwise unspecified and may change between minor versions.
//
// Use only when building cursors manually (e.g. for the FIRST page's
// "start from this synthetic position"). Paginate calls this
// internally on the last row's cursor_key.
func EncodeCursor[K any](key K) (string, error) {
	raw, err := json.Marshal(key)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// DecodeCursor decodes an opaque cursor previously produced by
// [Paginate] / [EncodeCursor] back into the original value. Empty
// cursor → zero value (the kit's "no cursor — first page" sentinel).
//
//	type userKey struct{ ID string }
//	prev, _ := db.DecodeCursor[userKey](req.Cursor)
//	page, _ := db.Paginate[User, userKey](ctx, svc.DB, sql, 20, scan, prev.ID)
//
// Returns *errs.Error{Kind: KindValidation, Code: "db_cursor_invalid"}
// on malformed input — that surfaces as HTTP 400 through `errs.HTTP`.
func DecodeCursor[K any](cursor string) (K, error) {
	var zero K
	if strings.TrimSpace(cursor) == "" {
		return zero, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return zero, errs.Wrap(err, errs.KindValidation, "db_cursor_invalid",
			"db.DecodeCursor: malformed cursor")
	}
	var out K
	if err := json.Unmarshal(raw, &out); err != nil {
		return zero, errs.Wrap(err, errs.KindValidation, "db_cursor_invalid",
			"db.DecodeCursor: cursor decode")
	}
	return out, nil
}

// suppress unused-import warning when callers strip helpers and
// errors stops being touched directly.
var _ = errors.New
