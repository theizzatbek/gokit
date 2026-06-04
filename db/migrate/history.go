package migrate

import (
	"context"
	"time"

	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/errs"
)

// AppliedRecord is one row from `schema_migrations`. Same field shape
// as [Status] but populated from the table rather than parsed Ups —
// History does NOT need fsys.
type AppliedRecord struct {
	Version   string
	Name      string
	AppliedAt time.Time
}

// History returns every applied migration, newest first. Drives the
// `/admin/migrations` endpoint pattern — operators see the audit
// trail without having to reach into psql.
//
// Returns an empty slice (NOT nil-with-error) when nothing is applied
// yet, even when the schema_migrations table is itself absent —
// bootstrap is called first so the table is materialised on demand.
//
// Errors flow as *errs.Error{KindInternal, Code: CodeTrackFailed}.
func History(ctx context.Context, d *db.DB) ([]AppliedRecord, error) {
	if err := bootstrap(ctx, d); err != nil {
		return nil, err
	}
	rows, err := d.Query(ctx,
		`SELECT version, name, applied_at FROM schema_migrations ORDER BY version DESC`)
	if err != nil {
		return nil, errs.Wrap(err, errs.KindInternal, CodeTrackFailed,
			"migrate: history select")
	}
	defer rows.Close()
	out := make([]AppliedRecord, 0)
	for rows.Next() {
		var r AppliedRecord
		if err := rows.Scan(&r.Version, &r.Name, &r.AppliedAt); err != nil {
			return nil, errs.Wrap(err, errs.KindInternal, CodeTrackFailed,
				"migrate: history scan")
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, errs.Wrap(err, errs.KindInternal, CodeTrackFailed,
			"migrate: history rows.Err")
	}
	return out, nil
}
