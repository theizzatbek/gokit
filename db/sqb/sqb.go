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

// errRow defers a build-time error until Scan so QueryRow has the same
// "always returns a pgx.Row" ergonomics as pgx itself.
type errRow struct{ err error }

func (r errRow) Scan(...any) error { return r.err }
