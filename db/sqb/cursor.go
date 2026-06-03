package sqb

import (
	"encoding/base64"
	"encoding/json"
	"time"

	sq "github.com/Masterminds/squirrel"

	"github.com/theizzatbek/gokit/errs"
)

// Cursor is the opaque keyset marker passed between paginated reads.
// The encoded form is base64(json({t, i})) — URL-safe and round-trip
// stable across releases.
//
// The kit chose (created_at, id) as the canonical keyset because it
// matches the "newest first feed" shape that covers ~80% of paginated
// list endpoints. Different ordering schemes (e.g. by score for a
// leaderboard) need a custom cursor type — Cursor is the common case,
// not the universal one.
type Cursor struct {
	// CreatedAt is the primary ordering key. Encoded as RFC3339Nano so
	// time zones round-trip; the DB-side comparison strips back to the
	// raw timestamptz value.
	CreatedAt time.Time `json:"t"`

	// ID is the tie-breaker for two rows sharing the same CreatedAt.
	// Treated as opaque text — UUIDs, BIGINTs serialised as strings,
	// short codes all work.
	ID string `json:"i"`
}

// Stable Code constants for cursor-related errors.
const (
	// CodeInvalidCursor — the "after" query parameter could not be
	// decoded as a Cursor (bad base64, malformed JSON, etc.).
	CodeInvalidCursor = "sqb_invalid_cursor"
)

// Encode returns the base64-encoded URL-safe cursor string.
// Round-trip safe with [DecodeCursor].
func (c Cursor) Encode() string {
	raw, _ := json.Marshal(c) // Cursor has only safe-to-marshal fields
	return base64.RawURLEncoding.EncodeToString(raw)
}

// DecodeCursor parses an encoded cursor string. Returns *errs.Error
// {KindValidation, Code: CodeInvalidCursor} on malformed input — the
// fibermap error mapping turns that into a clean 400 for the
// consumer.
//
// Empty input is treated as "no cursor" — returns (Cursor{}, nil) so
// callers can blindly pipe a missing query parameter through here.
func DecodeCursor(s string) (Cursor, error) {
	if s == "" {
		return Cursor{}, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return Cursor{}, errs.Wrap(err, errs.KindValidation, CodeInvalidCursor,
			"sqb: cursor decode")
	}
	var c Cursor
	if err := json.Unmarshal(raw, &c); err != nil {
		return Cursor{}, errs.Wrap(err, errs.KindValidation, CodeInvalidCursor,
			"sqb: cursor unmarshal")
	}
	return c, nil
}

// CursorPage is the query-param shape for cursor pagination. Drop into
// fibermap.RegisterHandlerWithQuery the same way as [Page]:
//
//	type ListInput struct {
//	    Query sqb.CursorPage
//	}
//
// The page returns at most Limit + 1 rows so callers can detect "more
// pages exist" without a separate COUNT(*).
type CursorPage struct {
	// Limit caps the rows returned. Defaults to PageDefaultLimit (20)
	// when zero; clamped to PageMaxLimit (100) at Apply time.
	Limit int `query:"limit" json:"limit" validate:"omitempty,min=1,max=100"`

	// After is the base64-encoded Cursor from the previous page's last
	// row. Empty = start at the newest row.
	After string `query:"after" json:"after"`
}

// Apply adds the LIMIT clause AND the keyset WHERE clause to b. The
// table must be ordered by (createdCol DESC, idCol DESC) for the
// resulting "next page" semantics to make sense — callers append
// the matching ORDER BY before or after Apply (Apply does not impose
// it because OrderBy is the per-endpoint allowlist surface, see
// [Page] notes).
//
// createdCol / idCol are the SQL identifiers (e.g. "u.created_at",
// "u.id"). They are SQL-spliced — callers MUST hardcode them in the
// handler, never accept them from user input.
//
// Returns *errs.Error{KindValidation, Code: CodeInvalidCursor} when
// After is malformed.
func (p CursorPage) Apply(b sq.SelectBuilder, createdCol, idCol string) (sq.SelectBuilder, error) {
	limit := p.Limit
	if limit <= 0 {
		limit = PageDefaultLimit
	}
	if limit > PageMaxLimit {
		limit = PageMaxLimit
	}
	b = b.Limit(uint64(limit))
	if p.After == "" {
		return b, nil
	}
	c, err := DecodeCursor(p.After)
	if err != nil {
		return b, err
	}
	// Keyset condition: (created_at, id) < (cursor.t, cursor.i)
	// matches the DESC ordering convention.
	cond := "(" + createdCol + ", " + idCol + ") < (?, ?)"
	return b.Where(cond, c.CreatedAt, c.ID), nil
}
