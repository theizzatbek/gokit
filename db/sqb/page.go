package sqb

import (
	sq "github.com/Masterminds/squirrel"
)

// Page is the standard query-param shape for paginated list endpoints.
// Fields use `query:` tags so the type drops straight into
// fibermap.RegisterHandlerWithQuery / RegisterHandlerWithInput, and
// validate tags so go-playground/validator catches obviously-wrong
// values before they reach the SQL layer:
//
//	type ListInput struct {
//	    Query db.Page
//	}
//	fibermap.RegisterHandlerWithInput(eng, "items.list",
//	    func(c *Context[T], in ListInput) error {
//	        b := sqb.Builder.Select(itemColumns...).From("items").
//	            OrderBy("created_at DESC")  // sort is the caller's job — see Notes
//	        b = in.Query.Apply(b)
//	        rows, err := sqb.Query(ctx, db, b)
//	        ...
//	    })
//
// Notes:
//
//   - ORDER BY is intentionally NOT part of Page. Sort columns are an
//     SQL-injection surface; the caller decides the allowlist and
//     appends OrderBy themselves.
//   - Limit defaults to 20 when zero. Apply clamps Limit to a hard cap
//     of 100 even when validation is bypassed — belt-and-suspenders
//     against an upstream sending Limit=10000.
//   - Offset is clamped to >=0 inside Apply. Validation also enforces
//     this; the runtime clamp catches direct constructions in tests.
type Page struct {
	Limit  int `query:"limit"  json:"limit"  validate:"omitempty,min=1,max=100"`
	Offset int `query:"offset" json:"offset" validate:"omitempty,min=0"`
}

// PageDefaultLimit is the page size used when Page.Limit is zero.
const PageDefaultLimit = 20

// PageMaxLimit is the runtime ceiling applied by Page.Apply regardless
// of the validation tag — services that disable the validator or
// bypass it still get a safe upper bound.
const PageMaxLimit = 100

// Apply adds LIMIT and OFFSET to b. Defaults: Limit zero or negative
// → PageDefaultLimit; Limit > PageMaxLimit clamped; Offset negative
// clamped to 0.
func (p Page) Apply(b sq.SelectBuilder) sq.SelectBuilder {
	limit := p.Limit
	if limit <= 0 {
		limit = PageDefaultLimit
	}
	if limit > PageMaxLimit {
		limit = PageMaxLimit
	}
	offset := p.Offset
	if offset < 0 {
		offset = 0
	}
	return b.Limit(uint64(limit)).Offset(uint64(offset))
}
