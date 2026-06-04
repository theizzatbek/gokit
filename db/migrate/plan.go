package migrate

import (
	"context"
	"io/fs"

	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/errs"
)

// Stable error Code constants for the planning + targeted-Up/Down API.
const (
	// CodeUnknownTarget — UpTo/DownTo was asked to advance to a
	// version that isn't in the parsed set.
	CodeUnknownTarget = "migrate_unknown_target"
)

// Plan returns the pending Up migrations in version order — the set
// Up would apply. Useful for dry-run output and pre-flight CI gates
// ("are there any pending migrations on this commit?").
//
// Empty slice means everything is current.
func Plan(ctx context.Context, d *db.DB, fsys fs.FS) ([]Migration, error) {
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
	pending := make([]Migration, 0, len(ups))
	for _, m := range ups {
		if !applied[m.Version] {
			pending = append(pending, m)
		}
	}
	return pending, nil
}

// UpTo applies every pending migration with Version <= target. Useful
// for staged rollouts ("ratchet staging to version 0023 before
// flipping the app code").
//
// Returns CodeUnknownTarget when target isn't in the parsed Ups.
// Already-applied versions are skipped (idempotent like Up).
//
// Pass [WithLock] for the same concurrent-boot serialisation as [Up].
func UpTo(ctx context.Context, d *db.DB, fsys fs.FS, target string, opts ...Option) error {
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
		if !containsVersion(ups, target) {
			return errs.NotFoundf(CodeUnknownTarget,
				"migrate: target %q not found in parsed Ups", target)
		}
		applied, err := appliedVersions(ctx, d)
		if err != nil {
			return err
		}
		for _, m := range ups {
			if m.Version > target {
				break
			}
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

// DownTo rolls back applied migrations until target is the highest
// applied version. The target itself is NOT rolled back.
//
// Useful for "roll back the last release" patterns where the
// operator knows the version they want to land on, not the count.
// Returns CodeUnknownTarget when target isn't a parsed Up.
func DownTo(ctx context.Context, d *db.DB, fsys fs.FS, target string) error {
	if err := bootstrap(ctx, d); err != nil {
		return err
	}
	ups, downs, err := Parse(fsys)
	if err != nil {
		return err
	}
	if !containsVersion(ups, target) {
		return errs.NotFoundf(CodeUnknownTarget,
			"migrate: target %q not found in parsed Ups", target)
	}
	// Fetch applied desc — we need them ordered newest-first.
	rows, err := d.Query(ctx,
		`SELECT version FROM schema_migrations WHERE version > $1 ORDER BY version DESC`, target)
	if err != nil {
		return errs.Wrap(err, errs.KindInternal, CodeTrackFailed,
			"migrate: select rollback set")
	}
	defer rows.Close()
	var toRoll []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return errs.Wrap(err, errs.KindInternal, CodeTrackFailed,
				"migrate: scan rollback set")
		}
		toRoll = append(toRoll, v)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, v := range toRoll {
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

func containsVersion(ups []Migration, target string) bool {
	for _, m := range ups {
		if m.Version == target {
			return true
		}
	}
	return false
}
