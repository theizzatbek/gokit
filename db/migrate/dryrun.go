package migrate

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"strings"

	"github.com/theizzatbek/gokit/db"
)

// Pending is a thin alias for [Plan] — returns the set of migrations
// that [Up] would apply on the next call. Kept separate from Plan so
// CI scripts can express intent ("does this branch carry pending
// migrations?") without dragging in the planning vocabulary.
//
// Empty slice means everything is current.
func Pending(ctx context.Context, d *db.DB, fsys fs.FS) ([]Migration, error) {
	return Plan(ctx, d, fsys)
}

// DryRun writes a human-readable summary of the pending Up migrations
// to w. No SQL is executed against d — DryRun is read-only, safe to
// run against production.
//
// Output format:
//
//	# 2 pending migrations
//
//	── 0042_add_users_email_index.sql ──────────────
//	CREATE INDEX users_email_idx ON users(email);
//
//	── 0043_add_orders_state.sql ──────────────
//	ALTER TABLE orders ADD COLUMN state text NOT NULL DEFAULT 'new';
//
// Use as a CI pre-flight gate ("run with `--dry-run` before each
// deploy and fail if the diff disagrees with what's expected") or as
// a kit `migrate plan` subcommand body.
//
// Returns the count of pending migrations + the first I/O error from w.
func DryRun(ctx context.Context, d *db.DB, fsys fs.FS, w io.Writer) (int, error) {
	pending, err := Plan(ctx, d, fsys)
	if err != nil {
		return 0, err
	}
	if _, err := fmt.Fprintf(w, "# %d pending migration%s\n",
		len(pending), pluralise(len(pending))); err != nil {
		return 0, err
	}
	if len(pending) == 0 {
		return 0, nil
	}
	for _, m := range pending {
		filename := m.Version + "_" + m.Name + ".sql"
		header := "── " + filename + " " + strings.Repeat("─", max(0, 40-len(filename)))
		if _, err := fmt.Fprintln(w); err != nil {
			return 0, err
		}
		if _, err := fmt.Fprintln(w, header); err != nil {
			return 0, err
		}
		body := strings.TrimRight(m.SQL, "\n")
		if _, err := fmt.Fprintln(w, body); err != nil {
			return 0, err
		}
	}
	return len(pending), nil
}

func pluralise(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
