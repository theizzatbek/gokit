// Package sqb is an opt-in squirrel query-builder wrapper preconfigured for
// Postgres ($N placeholders). The core db package does NOT import sqb —
// only the reverse direction is allowed.
package sqb

import (
	"context"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/theizzatbek/gokit/db"
)

// Builder is squirrel's statement builder preconfigured for Postgres
// ($N placeholders). Use this rather than sq.StatementBuilder directly.
var Builder = sq.StatementBuilder.PlaceholderFormat(sq.Dollar)

// SqlBuilder is the contract every squirrel builder satisfies — Select,
// Insert, Update, Delete all implement ToSql. Use it as the parameter
// type when you want to accept "any built statement".
type SqlBuilder interface {
	ToSql() (string, []any, error)
}

// Query runs any SqlBuilder against q and returns the rows. Typically
// used with sqb.Builder.Select(...), but also works with
// Update/Delete/Insert builders that have `Suffix("RETURNING ...")`.
// Errors flow through db.Querier.Query, which already maps pgx errors.
func Query(ctx context.Context, q db.Querier, b SqlBuilder) (pgx.Rows, error) {
	sql, args, err := b.ToSql()
	if err != nil {
		return nil, err
	}
	return q.Query(ctx, sql, args...)
}

// QueryRow runs any SqlBuilder and returns a single-row scanner. Typical
// use is INSERT/UPDATE … RETURNING into a single row:
//
//	var id string
//	err := sqb.QueryRow(ctx, d, sqb.Builder.
//	    Insert("users").Columns("email").Values(email).
//	    Suffix("RETURNING id")).Scan(&id)
//
// On zero rows the underlying db.Querier surfaces pgx.ErrNoRows as
// *errs.Error{KindNotFound}.
func QueryRow(ctx context.Context, q db.Querier, b SqlBuilder) pgx.Row {
	sql, args, err := b.ToSql()
	if err != nil {
		return errRow{err}
	}
	return q.QueryRow(ctx, sql, args...)
}

// Exec runs a mutating builder against any Querier.
func Exec(ctx context.Context, q db.Querier, b SqlBuilder) (pgconn.CommandTag, error) {
	sql, args, err := b.ToSql()
	if err != nil {
		return pgconn.CommandTag{}, err
	}
	return q.Exec(ctx, sql, args...)
}

// QueryAll runs b, iterates the rows through scan, and returns the
// collected slice. It dissolves the standard pgx scan-loop boilerplate
// (Query → defer Close → for Next → Scan → rows.Err) into one call:
//
//	items, err := sqb.QueryAll[Item](ctx, db, p.Apply(b), scanItem)
//
// scan receives one pgx.Row per iteration (pgx.Rows satisfies that
// interface — it has Scan(dest ...any) error). The same scan helper
// you write for sqb.QueryOne / direct rows.Scan works here.
//
// Returns nil slice + error if the underlying Query fails or any Scan
// errors. rows.Err() is checked after iteration.
func QueryAll[T any](ctx context.Context, q db.Querier, b SqlBuilder, scan func(pgx.Row, *T) error) ([]T, error) {
	rows, err := Query(ctx, q, b)
	if err != nil {
		return nil, err
	}
	return ScanAll[T](rows, scan)
}

// QueryOne is the single-row companion to QueryAll. Builds the
// statement, runs QueryRow, calls scan once, returns the populated T.
// Typical use is INSERT/UPDATE … RETURNING into a record or a SELECT
// by unique key:
//
//	user, err := sqb.QueryOne[User](ctx, db,
//	    sqb.Builder.Insert("users").Columns("email").Values(email).
//	        Suffix("RETURNING id, email, created_at"),
//	    scanUser)
//
// On zero rows the underlying QueryRow surfaces pgx.ErrNoRows as
// *errs.Error{KindNotFound} through db.Querier — scan receives the
// error via rows.Scan(...) and QueryOne returns the zero T + err.
func QueryOne[T any](ctx context.Context, q db.Querier, b SqlBuilder, scan func(pgx.Row, *T) error) (T, error) {
	var t T
	row := QueryRow(ctx, q, b)
	if err := scan(row, &t); err != nil {
		var zero T
		return zero, err
	}
	return t, nil
}

// ScanAll drains pre-fetched rows into a []T using scan. Use it when
// you ran the Query yourself (e.g. via *db.DB.ReadQuery for replica
// routing, or to chain with custom transport-level wrappers) and want
// the scan-loop folded:
//
//	rows, err := s.db.ReadQuery(ctx, sqlStr, args...)
//	if err != nil { return nil, err }
//	return sqb.ScanAll[Item](rows, scanItem)
//
// rows is closed automatically. rows.Err() is checked after iteration.
func ScanAll[T any](rows pgx.Rows, scan func(pgx.Row, *T) error) ([]T, error) {
	defer rows.Close()
	var out []T
	for rows.Next() {
		var t T
		if err := scan(rows, &t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// errRow defers a build-time error until Scan so QueryRow has the same
// "always returns a pgx.Row" ergonomics as pgx itself.
type errRow struct{ err error }

func (r errRow) Scan(...any) error { return r.err }
