package db

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// pgxQuerier abstracts the subset of pgx.Tx and *pgxpool.Pool we need so the
// shared helpers can target either.
type pgxQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func doQuery(ctx context.Context, q pgxQuerier, sql string, args ...any) (pgx.Rows, error) {
	rows, err := q.Query(ctx, sql, args...)
	return rows, mapPgxErr(err)
}

func doExec(ctx context.Context, q pgxQuerier, sql string, args ...any) (pgconn.CommandTag, error) {
	tag, err := q.Exec(ctx, sql, args...)
	return tag, mapPgxErr(err)
}

// mappedRow wraps a pgx.Row so Scan's error is funneled through mapPgxErr.
type mappedRow struct{ inner pgx.Row }

func (r mappedRow) Scan(dst ...any) error {
	return mapPgxErr(r.inner.Scan(dst...))
}

func doQueryRow(ctx context.Context, q pgxQuerier, sql string, args ...any) pgx.Row {
	return mappedRow{inner: q.QueryRow(ctx, sql, args...)}
}
