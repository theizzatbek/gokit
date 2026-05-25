package db

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/theizzatbek/gokit/errs"
)

// mapPgxErr is the single funnel through which every pgx error passes before
// reaching a caller. Unrecognised SQLSTATE codes fall through to KindInternal
// so the caller still sees a typed *errs.Error and can errors.As back to
// *pgconn.PgError for inspection.
func mapPgxErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return errs.Wrap(err, errs.KindNotFound, "not_found", "row not found")
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return errs.Wrap(err, errs.KindTimeout, "db_timeout", "query canceled or timed out")
	}

	if pg, ok := errors.AsType[*pgconn.PgError](err); ok {
		switch pg.Code {
		case "23505":
			return errs.Wrap(err, errs.KindAlreadyExists, "already_exists", "unique constraint violated")
		case "23503":
			return errs.Wrap(err, errs.KindConflict, "fk_violation", "foreign key violated")
		case "40001":
			return errs.Wrap(err, errs.KindConflict, "tx_conflict", "serialization failure (retry safe)")
		case "40P01":
			return errs.Wrap(err, errs.KindConflict, "tx_conflict", "deadlock detected (retry safe)")
		case "57014":
			return errs.Wrap(err, errs.KindTimeout, "db_timeout", "query canceled by server")
		case "08000", "08001", "08003", "08004", "08006":
			return errs.Wrap(err, errs.KindUnavailable, "db_unavailable", "connection error")
		}
	}
	return errs.Wrap(err, errs.KindInternal, "db_failure", "database operation failed")
}
