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

// Query runs a SelectBuilder against any Querier. Errors flow through
// db.Querier.Query, which already maps pgx errors.
func Query(ctx context.Context, q db.Querier, b sq.SelectBuilder) (pgx.Rows, error) {
	sql, args, err := b.ToSql()
	if err != nil {
		return nil, err
	}
	return q.Query(ctx, sql, args...)
}

// SqlBuilder is the squirrel interface shared by Insert/Update/Delete builders.
type SqlBuilder interface {
	ToSql() (string, []any, error)
}

// Exec runs a mutating builder against any Querier.
func Exec(ctx context.Context, q db.Querier, b SqlBuilder) (pgconn.CommandTag, error) {
	sql, args, err := b.ToSql()
	if err != nil {
		return pgconn.CommandTag{}, err
	}
	return q.Exec(ctx, sql, args...)
}
