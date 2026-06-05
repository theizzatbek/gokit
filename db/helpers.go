package db

import (
	"context"
	"errors"

	"github.com/theizzatbek/gokit/errs"
)

// Exists is sugar for `SELECT EXISTS(<sql>)` — wraps the supplied
// query body in an EXISTS clause and returns the boolean. The kit
// applies the same `mapPgxErr` mapping as `*DB.QueryRow`, so transport
// failures still surface as `*errs.Error{KindUnavailable}` etc.
//
// `sql` must be the INNER body of the EXISTS check — i.e. a full
// `SELECT 1 FROM …` statement. The kit prepends `SELECT EXISTS(` and
// appends `)` itself; do NOT wrap your query manually.
//
//	ok, err := db.Exists(ctx, svc.DB,
//	    `SELECT 1 FROM users WHERE email = $1`, email)
//
// Works against *DB and *Tx (and any other Querier) interchangeably.
func Exists(ctx context.Context, q Querier, sql string, args ...any) (bool, error) {
	if q == nil {
		return false, errs.Validation("db_nil_querier", "db.Exists: querier is nil")
	}
	var ok bool
	if err := q.QueryRow(ctx, "SELECT EXISTS("+sql+")", args...).Scan(&ok); err != nil {
		return false, err
	}
	return ok, nil
}

// Count runs a `SELECT count(*)` query body and returns the result as
// an int64. Like [Exists], the caller supplies the body — Count
// wraps it in `SELECT count(*) FROM (…) _` so an arbitrary inner
// SELECT works:
//
//	n, err := db.Count(ctx, svc.DB,
//	    `SELECT 1 FROM events WHERE created_at >= $1`, since)
//
// The supplied query body is rendered verbatim — the kit does NOT
// inject `FROM`; the caller's SQL must already include the FROM /
// JOIN / WHERE clauses they need. The subquery form is what lets
// Count piggyback on arbitrary filter logic without parsing SQL.
func Count(ctx context.Context, q Querier, sql string, args ...any) (int64, error) {
	if q == nil {
		return 0, errs.Validation("db_nil_querier", "db.Count: querier is nil")
	}
	var n int64
	wrapped := "SELECT count(*) FROM (" + sql + ") AS _kit_count_subq"
	if err := q.QueryRow(ctx, wrapped, args...).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// Pluck runs `sql` and Scan's every row's FIRST column into a *T,
// returning the assembled slice. Generic — call sites pick T:
//
//	ids, err := db.Pluck[string](ctx, svc.DB,
//	    `SELECT id FROM users WHERE org_id = $1`, orgID)
//	totals, err := db.Pluck[int64](ctx, svc.DB,
//	    `SELECT count(*) FROM orders GROUP BY user_id`)
//
// Use for any "SELECT a single column → []T" pattern; rows.Close is
// deferred internally. Empty result returns an empty slice (not nil)
// so callers can `for _, x := range got { … }` unconditionally.
//
// The kit does NOT cap result count — wrap with `LIMIT N` upstream
// when bounding result size matters.
func Pluck[T any](ctx context.Context, q Querier, sql string, args ...any) ([]T, error) {
	if q == nil {
		return nil, errs.Validation("db_nil_querier", "db.Pluck: querier is nil")
	}
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]T, 0, 8)
	for rows.Next() {
		var v T
		if err := rows.Scan(&v); err != nil {
			return nil, mapPgxErr(err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, mapPgxErr(err)
	}
	return out, nil
}

// Get is the single-row variant of [Pluck]: Scans the FIRST column of
// the FIRST row into a *T, returning *errs.Error{KindNotFound} on an
// empty result.
//
//	email, err := db.Get[string](ctx, svc.DB,
//	    `SELECT email FROM users WHERE id = $1`, id)
//
// Use for "is this column set?" / "what's the canonical X for Y?"
// lookups where ergonomic `var x T; QueryRow().Scan(&x)` would otherwise
// be three lines.
func Get[T any](ctx context.Context, q Querier, sql string, args ...any) (T, error) {
	var zero T
	if q == nil {
		return zero, errs.Validation("db_nil_querier", "db.Get: querier is nil")
	}
	var v T
	if err := q.QueryRow(ctx, sql, args...).Scan(&v); err != nil {
		return zero, err
	}
	return v, nil
}

// NotFound reports whether err is a `*errs.Error` with
// `Kind == KindNotFound` — the kit's canonical "no rows" mapping.
// Convenience over `errors.As + .Kind == NotFound` boilerplate at
// call sites.
//
//	if _, err := svc.DB.QueryRow(...).Scan(&x); err != nil {
//	    if db.NotFound(err) {
//	        return nil // expected; treat as empty
//	    }
//	    return err
//	}
func NotFound(err error) bool {
	if err == nil {
		return false
	}
	var e *errs.Error
	if errors.As(err, &e) {
		return e.Kind == errs.KindNotFound
	}
	return false
}
