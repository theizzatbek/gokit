package db

import (
	"context"
	"strconv"
	"strings"

	"github.com/theizzatbek/gokit/errs"
)

// BulkUpdate is the builder for the single-round-trip
// `UPDATE table SET … FROM (VALUES …) AS t(…) WHERE table.key = t.key`
// pattern — useful for "I have N rows and N corresponding new values
// to write" cases where issuing N UPDATEs would be wasteful.
//
// Usage:
//
//	bu := db.NewBulkUpdate("users", "id")
//	bu.Columns("email", "display_name")
//	bu.Add("u-1", "alice@new.example", "Alice")
//	bu.Add("u-2", "bob@new.example",   "Bob")
//	n, err := bu.Exec(ctx, svc.DB)
//	// n = 2 (rows actually changed)
//
// For thousands of rows prefer [DB.CopyFrom] into a temp table + a
// single UPDATE — the VALUES list grows the parsed-statement size
// linearly with row count. The kit picks ~500 rows as a soft ceiling;
// over that the helper still works but logs a warning when a logger
// is wired via Auth-side observability.
type BulkUpdate struct {
	table   string
	keyCol  string
	columns []string
	rows    [][]any
}

// NewBulkUpdate starts a builder against the named table, with the
// `keyCol` being the primary-key column the UPDATE joins on. Both
// values are inlined verbatim into the generated SQL — DO NOT pass
// untrusted input; the kit does no SQL-escaping.
func NewBulkUpdate(table, keyCol string) *BulkUpdate {
	return &BulkUpdate{table: table, keyCol: keyCol}
}

// Columns names the columns to update. Order matters — every
// subsequent [Add] call MUST supply values in the same order
// (keyVal, then one per Columns entry).
//
// Calling Columns more than once replaces the list. Empty Columns at
// Exec time is a kit error: "what would you even be updating?".
func (b *BulkUpdate) Columns(cols ...string) *BulkUpdate {
	b.columns = append(b.columns[:0], cols...)
	return b
}

// Add appends one row to the bulk update. `key` is the PK value; the
// remaining args are the new column values in [Columns] order. The
// number of args MUST equal `1 + len(columns)`; mismatches surface at
// [BulkUpdate.Exec] time so you can build conditionally.
func (b *BulkUpdate) Add(key any, vals ...any) *BulkUpdate {
	row := make([]any, 0, 1+len(vals))
	row = append(row, key)
	row = append(row, vals...)
	b.rows = append(b.rows, row)
	return b
}

// Len returns the number of rows queued. Useful for asserts in
// callers that build the bulk-update conditionally.
func (b *BulkUpdate) Len() int { return len(b.rows) }

// Exec ships the bulk-update statement against the supplied
// [Querier]. Returns the count of rows affected (Postgres counts
// every row whose WHERE matched, even if its column values were
// unchanged — there is no "actually different" filter unless you
// add one to the WHERE).
//
// Empty bulk (no Add calls) is a no-op — returns (0, nil) without
// touching the DB.
//
// Pre-flight validation: Columns set must be non-empty; every row
// must have `1 + len(Columns)` args; table/keyCol must not be
// empty. Failures surface as *errs.Error with Kind=KindValidation
// and stable Code prefixes (`db_bulk_*`).
func (b *BulkUpdate) Exec(ctx context.Context, q Querier) (int64, error) {
	if q == nil {
		return 0, errs.Validation("db_nil_querier", "db.BulkUpdate.Exec: querier is nil")
	}
	if b.table == "" {
		return 0, errs.Validation("db_bulk_no_table", "db.BulkUpdate: table is empty")
	}
	if b.keyCol == "" {
		return 0, errs.Validation("db_bulk_no_key", "db.BulkUpdate: keyCol is empty")
	}
	if len(b.columns) == 0 {
		return 0, errs.Validation("db_bulk_no_columns", "db.BulkUpdate: Columns() not called")
	}
	if len(b.rows) == 0 {
		return 0, nil
	}
	expected := 1 + len(b.columns)
	for i, r := range b.rows {
		if len(r) != expected {
			return 0, errs.Validationf("db_bulk_row_arity",
				"db.BulkUpdate: row %d has %d args, want %d (key + %d columns)",
				i, len(r), expected, len(b.columns))
		}
	}

	sql, args := b.buildSQL()
	tag, err := q.Exec(ctx, sql, args...)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// buildSQL assembles the parameterised statement + flat args slice.
// Split out for testability — unit tests can probe the rendered SQL
// without running it against Postgres.
func (b *BulkUpdate) buildSQL() (string, []any) {
	// SET clause: `email = t.email, display_name = t.display_name`
	setParts := make([]string, 0, len(b.columns))
	for _, c := range b.columns {
		setParts = append(setParts, c+" = t."+c)
	}

	// VALUES clause: `($1, $2, $3), ($4, $5, $6), ...`
	args := make([]any, 0, len(b.rows)*(1+len(b.columns)))
	valueGroups := make([]string, 0, len(b.rows))
	idx := 1
	for _, row := range b.rows {
		placeholders := make([]string, 0, len(row))
		for range row {
			placeholders = append(placeholders, "$"+strconv.Itoa(idx))
			idx++
		}
		args = append(args, row...)
		valueGroups = append(valueGroups, "("+strings.Join(placeholders, ", ")+")")
	}

	// t-table column list: `t(id, email, display_name)`
	tCols := make([]string, 0, 1+len(b.columns))
	tCols = append(tCols, b.keyCol)
	tCols = append(tCols, b.columns...)

	sql := "UPDATE " + b.table +
		" SET " + strings.Join(setParts, ", ") +
		" FROM (VALUES " + strings.Join(valueGroups, ", ") + ") AS t(" + strings.Join(tCols, ", ") + ")" +
		" WHERE " + b.table + "." + b.keyCol + " = t." + b.keyCol
	return sql, args
}
