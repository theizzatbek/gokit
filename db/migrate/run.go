package migrate

import (
	"context"
	"io/fs"

	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/errs"
)

// schemaMigrationsDDL bootstraps the tracking table. Idempotent — the
// runner calls it on every Up.
const schemaMigrationsDDL = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version    text        PRIMARY KEY,
    name       text        NOT NULL,
    applied_at timestamptz NOT NULL DEFAULT NOW()
)
`

// Up applies every pending migration parsed from fsys, in version
// order. Already-applied versions are skipped (idempotent). Each
// migration runs in its own transaction unless the file carries
// the `-- @migrate:no-transaction` directive (for CREATE INDEX
// CONCURRENTLY and similar).
//
// On the first failure Up stops and returns the wrapped error.
// Already-applied migrations from the same call stay committed —
// the runner does NOT roll back the whole batch.
//
// Pass [WithLock] to serialise concurrent boot races between
// replicas — only one holds the advisory lock at a time, the rest
// block on it and then drain a (now-empty) pending set as a no-op.
func Up(ctx context.Context, d *db.DB, fsys fs.FS, opts ...Option) error {
	var cfg applyConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	return applyWithOptionalLock(ctx, d, cfg, func(ctx context.Context) error {
		if err := bootstrap(ctx, d); err != nil {
			return err
		}
		ups, _, err := Parse(fsys)
		if err != nil {
			return err
		}
		applied, err := appliedVersions(ctx, d)
		if err != nil {
			return err
		}
		for _, m := range ups {
			if applied[m.Version] {
				continue
			}
			if err := applyOne(ctx, d, m); err != nil {
				return err
			}
		}
		return nil
	})
}

// Down rolls back the n most recently applied migrations in reverse
// order. n <= 0 is a no-op. Returns CodeUnknownDown when a
// rolled-back version has no matching .down.sql.
//
// Down does NOT consult `applied_at` for ordering — version-string
// descending is the contract. Mixed-width prefixes can therefore
// produce surprising orderings; the kit recommends sticking to
// zero-padded NNNN.
func Down(ctx context.Context, d *db.DB, fsys fs.FS, n int) error {
	if n <= 0 {
		return nil
	}
	if err := bootstrap(ctx, d); err != nil {
		return err
	}
	_, downs, err := Parse(fsys)
	if err != nil {
		return err
	}
	versions, err := appliedDesc(ctx, d, n)
	if err != nil {
		return err
	}
	for _, v := range versions {
		dm, ok := downs[v]
		if !ok {
			return errs.NotFoundf(CodeUnknownDown,
				"migrate: no .down.sql for version %q", v)
		}
		if err := rollbackOne(ctx, d, dm); err != nil {
			return err
		}
	}
	return nil
}

// Version returns the highest applied version, or "" when nothing
// is applied (including when the schema_migrations table itself is
// absent — bootstrap defers creation to Up/Down).
func Version(ctx context.Context, d *db.DB) (string, error) {
	if err := bootstrap(ctx, d); err != nil {
		return "", err
	}
	var v string
	row := d.QueryRow(ctx,
		`SELECT version FROM schema_migrations ORDER BY version DESC LIMIT 1`)
	if err := row.Scan(&v); err != nil {
		// db.QueryRow maps pgx.ErrNoRows to *errs.Error{NotFound}.
		var e *errs.Error
		if isNotFound(err, &e) {
			return "", nil
		}
		return "", err
	}
	return v, nil
}

// Status is one row in [List]'s output — every parsed Up plus an
// `Applied` boolean drawn from schema_migrations.
type Status struct {
	Version string
	Name    string
	Applied bool
}

// List returns the parsed Ups in version order with their applied
// status. Useful for `kit migrate status` style introspection tools.
func List(ctx context.Context, d *db.DB, fsys fs.FS) ([]Status, error) {
	if err := bootstrap(ctx, d); err != nil {
		return nil, err
	}
	ups, _, err := Parse(fsys)
	if err != nil {
		return nil, err
	}
	applied, err := appliedVersions(ctx, d)
	if err != nil {
		return nil, err
	}
	out := make([]Status, 0, len(ups))
	for _, m := range ups {
		out = append(out, Status{
			Version: m.Version,
			Name:    m.Name,
			Applied: applied[m.Version],
		})
	}
	return out, nil
}

func bootstrap(ctx context.Context, d *db.DB) error {
	if _, err := d.Exec(ctx, schemaMigrationsDDL); err != nil {
		return errs.Wrap(err, errs.KindInternal, CodeBootstrapFailed,
			"migrate: schema_migrations bootstrap")
	}
	return nil
}

func appliedVersions(ctx context.Context, d *db.DB) (map[string]bool, error) {
	rows, err := d.Query(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, errs.Wrap(err, errs.KindInternal, CodeTrackFailed,
			"migrate: select schema_migrations")
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, errs.Wrap(err, errs.KindInternal, CodeTrackFailed,
				"migrate: scan schema_migrations")
		}
		out[v] = true
	}
	return out, rows.Err()
}

func appliedDesc(ctx context.Context, d *db.DB, limit int) ([]string, error) {
	rows, err := d.Query(ctx,
		`SELECT version FROM schema_migrations ORDER BY version DESC LIMIT $1`, limit)
	if err != nil {
		return nil, errs.Wrap(err, errs.KindInternal, CodeTrackFailed,
			"migrate: select recent")
	}
	defer rows.Close()
	out := make([]string, 0, limit)
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, errs.Wrap(err, errs.KindInternal, CodeTrackFailed,
				"migrate: scan recent")
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func applyOne(ctx context.Context, d *db.DB, m Migration) error {
	track := func(q db.Querier) error {
		_, err := q.Exec(ctx,
			`INSERT INTO schema_migrations (version, name) VALUES ($1, $2)`,
			m.Version, m.Name)
		if err != nil {
			return errs.Wrap(err, errs.KindInternal, CodeTrackFailed,
				"migrate: track "+m.Version)
		}
		return nil
	}
	if m.NoTransaction {
		if _, err := d.Exec(ctx, m.SQL); err != nil {
			return errs.Wrap(err, errs.KindInternal, CodeApplyFailed,
				"migrate: apply "+m.Version+" "+m.Name)
		}
		return track(d)
	}
	return d.Tx(ctx, func(tx *db.Tx) error {
		if _, err := tx.Exec(ctx, m.SQL); err != nil {
			return errs.Wrap(err, errs.KindInternal, CodeApplyFailed,
				"migrate: apply "+m.Version+" "+m.Name)
		}
		return track(tx)
	})
}

func rollbackOne(ctx context.Context, d *db.DB, m Migration) error {
	untrack := func(q db.Querier) error {
		_, err := q.Exec(ctx,
			`DELETE FROM schema_migrations WHERE version = $1`, m.Version)
		if err != nil {
			return errs.Wrap(err, errs.KindInternal, CodeTrackFailed,
				"migrate: untrack "+m.Version)
		}
		return nil
	}
	if m.NoTransaction {
		if _, err := d.Exec(ctx, m.SQL); err != nil {
			return errs.Wrap(err, errs.KindInternal, CodeRollbackFailed,
				"migrate: rollback "+m.Version+" "+m.Name)
		}
		return untrack(d)
	}
	return d.Tx(ctx, func(tx *db.Tx) error {
		if _, err := tx.Exec(ctx, m.SQL); err != nil {
			return errs.Wrap(err, errs.KindInternal, CodeRollbackFailed,
				"migrate: rollback "+m.Version+" "+m.Name)
		}
		return untrack(tx)
	})
}

func isNotFound(err error, target **errs.Error) bool {
	for cur := err; cur != nil; {
		if e, ok := cur.(*errs.Error); ok {
			*target = e
			return e.Kind == errs.KindNotFound
		}
		if u, ok := cur.(interface{ Unwrap() error }); ok {
			cur = u.Unwrap()
			continue
		}
		return false
	}
	return false
}
