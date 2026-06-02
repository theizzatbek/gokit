package db

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// CopyFrom performs a bulk insert via the COPY protocol. Funnel errors
// through mapPgxErr so unique-constraint violations etc. surface as
// typed *errs.Error consistently with Query/Exec.
//
// `table` and `columns` map 1-to-1 to pgx's CopyFrom args; `src`
// usually comes from pgx.CopyFromRows for in-memory slices or a custom
// CopyFromSource implementation for streaming sources.
//
//	rowsAffected, err := svc.DB.CopyFrom(ctx,
//	    pgx.Identifier{"events"},
//	    []string{"id", "type", "payload"},
//	    pgx.CopyFromRows(batch),
//	)
//
// Returns the number of rows accepted by Postgres + the wrapped error.
// COPY runs OUTSIDE any transaction here — wrap Tx + CopyFrom together
// (via *Tx.CopyFrom) when you need atomicity with surrounding statements.
func (d *DB) CopyFrom(ctx context.Context, table pgx.Identifier, columns []string, src pgx.CopyFromSource) (int64, error) {
	n, err := d.pool.CopyFrom(ctx, table, columns, src)
	return n, mapPgxErr(err)
}

// CopyFrom inside a Tx — same shape as DB.CopyFrom but the COPY runs
// atomically with the surrounding transaction.
func (t *Tx) CopyFrom(ctx context.Context, table pgx.Identifier, columns []string, src pgx.CopyFromSource) (int64, error) {
	n, err := t.tx.CopyFrom(ctx, table, columns, src)
	return n, mapPgxErr(err)
}
