package sqb

import (
	"strings"

	sq "github.com/Masterminds/squirrel"

	"github.com/theizzatbek/gokit/errs"
)

// SortOrder is the direction part of a sort spec — ASC or DESC.
type SortOrder string

const (
	SortAsc  SortOrder = "ASC"
	SortDesc SortOrder = "DESC"
)

// SortSpec is one parsed (column, direction) pair after [ParseSort]
// has validated the field against an allowlist.
type SortSpec struct {
	// Column is the SQL identifier resolved from the allowlist — safe
	// to splice into ORDER BY.
	Column string
	// Order is the requested direction.
	Order SortOrder
}

// Stable Code constants for sort-related errors.
const (
	// CodeInvalidSort — a sort field was not in the allowlist, OR the
	// input string was malformed (empty field after split, etc.).
	CodeInvalidSort = "sqb_invalid_sort"
)

// ParseSort turns a comma-separated sort string ("name,-created_at")
// into a validated []SortSpec. Prefix "-" means DESC; bare name means
// ASC. allowed maps the public/API column name to its SQL column
// (use the same string when they coincide).
//
// Closes the SQL-injection gap noted on [Page]: callers no longer
// have to write a per-handler safelist by hand. Empty input returns
// (nil, nil) — caller decides the default ORDER BY.
//
// Returns *errs.Error{KindValidation, Code: CodeInvalidSort} on the
// first unknown field. The error message names the offending field
// so a 4xx response can guide the caller without manual wrapping.
func ParseSort(in string, allowed map[string]string) ([]SortSpec, error) {
	in = strings.TrimSpace(in)
	if in == "" {
		return nil, nil
	}
	parts := strings.Split(in, ",")
	out := make([]SortSpec, 0, len(parts))
	for _, raw := range parts {
		field := strings.TrimSpace(raw)
		if field == "" {
			return nil, errs.Validationf(CodeInvalidSort,
				"sqb: empty sort field in %q", in)
		}
		order := SortAsc
		if field[0] == '-' {
			order = SortDesc
			field = strings.TrimSpace(field[1:])
		}
		if field == "" {
			return nil, errs.Validationf(CodeInvalidSort,
				"sqb: empty sort field after %q prefix", "-")
		}
		col, ok := allowed[field]
		if !ok {
			return nil, errs.Validationf(CodeInvalidSort,
				"sqb: sort field %q not allowed", field)
		}
		out = append(out, SortSpec{Column: col, Order: order})
	}
	return out, nil
}

// ApplySort appends ORDER BY clauses to b in the order of specs.
// Empty specs returns b unchanged so callers can chain this after
// a default-ordering OrderBy without conflicting clauses.
func ApplySort(b sq.SelectBuilder, specs []SortSpec) sq.SelectBuilder {
	for _, s := range specs {
		b = b.OrderBy(s.Column + " " + string(s.Order))
	}
	return b
}

// Sort is the one-call combination of [ParseSort] + [ApplySort]:
//
//	b, err := sqb.Sort(builder, in.Sort, map[string]string{
//	    "name":       "u.name",
//	    "created_at": "u.created_at",
//	})
//	if err != nil { return err }
//	rows, err := sqb.Query(ctx, db, b)
//
// Empty input is a no-op (b returned unchanged, nil error).
func Sort(b sq.SelectBuilder, in string, allowed map[string]string) (sq.SelectBuilder, error) {
	specs, err := ParseSort(in, allowed)
	if err != nil {
		return b, err
	}
	return ApplySort(b, specs), nil
}
