package migrate

import (
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/theizzatbek/gokit/errs"
)

// Stable error Code constants returned by the migration runner.
const (
	// CodeReadFS — embed.FS read failed.
	CodeReadFS = "migrate_read_fs"

	// CodeInvalidFilename — file name doesn't match the
	// NNNN_name.sql / NNNN_name.down.sql convention.
	CodeInvalidFilename = "migrate_invalid_filename"

	// CodeDuplicateVersion — two Up files share the same NNNN
	// prefix. The runner refuses to apply ambiguous ordering.
	CodeDuplicateVersion = "migrate_duplicate_version"

	// CodeOrphanDown — a .down.sql file has no matching Up.
	CodeOrphanDown = "migrate_orphan_down"

	// CodeApplyFailed — a migration file's SQL execution failed.
	// The wrapped pgx error carries the original message.
	CodeApplyFailed = "migrate_apply_failed"

	// CodeRollbackFailed — a Down migration's SQL execution failed.
	CodeRollbackFailed = "migrate_rollback_failed"

	// CodeTrackFailed — INSERT/DELETE on schema_migrations failed.
	CodeTrackFailed = "migrate_track_failed"

	// CodeBootstrapFailed — schema_migrations CREATE TABLE failed.
	CodeBootstrapFailed = "migrate_bootstrap_failed"

	// CodeUnknownVersion — Down was asked to roll back a version
	// that has no Down file alongside it.
	CodeUnknownDown = "migrate_unknown_down"
)

// Migration is one parsed file in the embed.FS — the version key,
// human-readable name, raw SQL, and the transactional preference.
type Migration struct {
	Version       string // numeric NNNN prefix
	Name          string // remainder of the filename (sans extension)
	SQL           string // file contents
	NoTransaction bool   // -- @migrate:no-transaction directive present
	IsDown        bool   // true for .down.sql files
}

// filenameRE matches `NNNN_name(.down)?.sql`.
// - group 1 = version (must be all digits)
// - group 2 = remainder of the name
// - group 3 = ".down" or empty
var filenameRE = regexp.MustCompile(`^(\d+)_([A-Za-z0-9._-]+?)(\.down)?\.sql$`)

// noTxRE matches the no-transaction directive on the first non-blank,
// non-comment-noise line.
var noTxRE = regexp.MustCompile(`^\s*--\s*@migrate:no-transaction\s*$`)

// Parse walks fsys (typically an `embed.FS` of `migrations/`) and
// returns the deduplicated, ordered set of Up migrations + the
// matching Down lookup. fsys may contain unrelated files; only those
// matching the naming convention are picked up.
//
// Walking order: lexical (which, for zero-padded NNNN prefixes, is
// also numeric). Mixed-width prefixes (1_init.sql vs 0001_init.sql)
// are accepted but flagged as DuplicateVersion if they collide.
func Parse(fsys fs.FS) (ups []Migration, downs map[string]Migration, err error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		// Fall back to walking from the migrations/ subdir if the
		// caller supplied the root of the embed.FS rather than a sub.
		entries, err = fs.ReadDir(fsys, "migrations")
		if err != nil {
			return nil, nil, errs.Wrap(err, errs.KindInternal, CodeReadFS,
				"migrate: read fs")
		}
		fsys, err = fs.Sub(fsys, "migrations")
		if err != nil {
			return nil, nil, errs.Wrap(err, errs.KindInternal, CodeReadFS,
				"migrate: read fs sub")
		}
		// Re-read in the subbed FS view.
		entries, err = fs.ReadDir(fsys, ".")
		if err != nil {
			return nil, nil, errs.Wrap(err, errs.KindInternal, CodeReadFS,
				"migrate: read sub fs")
		}
	}
	downs = map[string]Migration{}
	upByVersion := map[string]Migration{}
	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		m, ok, err := parseEntry(fsys, ent.Name())
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			continue
		}
		if m.IsDown {
			if prev, exists := downs[m.Version]; exists {
				return nil, nil, errs.Validationf(CodeDuplicateVersion,
					"migrate: down %q duplicates %q", ent.Name(), prev.Name)
			}
			downs[m.Version] = m
			continue
		}
		if prev, exists := upByVersion[m.Version]; exists {
			return nil, nil, errs.Validationf(CodeDuplicateVersion,
				"migrate: %q duplicates version %q (also from %q)",
				ent.Name(), m.Version, prev.Name)
		}
		upByVersion[m.Version] = m
	}
	// Validate every down has a matching up.
	for v, d := range downs {
		if _, exists := upByVersion[v]; !exists {
			return nil, nil, errs.Validationf(CodeOrphanDown,
				"migrate: down %q has no matching up", d.Name)
		}
	}
	ups = make([]Migration, 0, len(upByVersion))
	for _, m := range upByVersion {
		ups = append(ups, m)
	}
	sort.Slice(ups, func(i, j int) bool { return ups[i].Version < ups[j].Version })
	return ups, downs, nil
}

// parseEntry classifies one file. Returns (zero, false, nil) when
// the file doesn't match the naming convention (silently skipped so
// README.md / docs alongside .sql files don't trip the parser).
func parseEntry(fsys fs.FS, name string) (Migration, bool, error) {
	base := path.Base(name)
	groups := filenameRE.FindStringSubmatch(base)
	if groups == nil {
		// Allow non-matching files (.md, .txt) silently. Only error on
		// .sql that's malformed.
		if strings.HasSuffix(strings.ToLower(base), ".sql") {
			return Migration{}, false, errs.Validationf(CodeInvalidFilename,
				"migrate: %q does not match NNNN_name(.down)?.sql", base)
		}
		return Migration{}, false, nil
	}
	raw, err := fs.ReadFile(fsys, name)
	if err != nil {
		return Migration{}, false, errs.Wrap(err, errs.KindInternal, CodeReadFS,
			"migrate: read "+base)
	}
	sql := string(raw)
	return Migration{
		Version:       groups[1],
		Name:          groups[2],
		SQL:           sql,
		NoTransaction: detectNoTxDirective(sql),
		IsDown:        groups[3] == ".down",
	}, true, nil
}

// detectNoTxDirective scans for `-- @migrate:no-transaction` on the
// first non-blank, non-blank-comment line. Whitespace and the
// directive's own leading comment marker are tolerated.
func detectNoTxDirective(sql string) bool {
	for _, line := range strings.Split(sql, "\n") {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		return noTxRE.MatchString(line)
	}
	return false
}
