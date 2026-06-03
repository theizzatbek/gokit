package sqb_test

import (
	"errors"
	"testing"
	"time"

	sq "github.com/Masterminds/squirrel"

	"github.com/theizzatbek/gokit/db/sqb"
	"github.com/theizzatbek/gokit/errs"
)

// ── Sort ──────────────────────────────────────────────────────────

func TestParseSort_EmptyInputReturnsNil(t *testing.T) {
	t.Parallel()
	got, err := sqb.ParseSort("", map[string]string{"name": "u.name"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func TestParseSort_BareFieldIsASC(t *testing.T) {
	t.Parallel()
	got, err := sqb.ParseSort("name", map[string]string{"name": "u.name"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Order != sqb.SortAsc || got[0].Column != "u.name" {
		t.Errorf("got %+v", got)
	}
}

func TestParseSort_DashPrefixIsDESC(t *testing.T) {
	t.Parallel()
	got, err := sqb.ParseSort("-created_at", map[string]string{
		"created_at": "u.created_at",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Order != sqb.SortDesc || got[0].Column != "u.created_at" {
		t.Errorf("got %+v", got)
	}
}

func TestParseSort_MultipleFields(t *testing.T) {
	t.Parallel()
	got, err := sqb.ParseSort("name,-created_at", map[string]string{
		"name":       "u.name",
		"created_at": "u.created_at",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Column != "u.name" || got[1].Column != "u.created_at" {
		t.Errorf("got %+v", got)
	}
	if got[0].Order != sqb.SortAsc || got[1].Order != sqb.SortDesc {
		t.Errorf("got orders %v %v", got[0].Order, got[1].Order)
	}
}

func TestParseSort_UnknownFieldErrors(t *testing.T) {
	t.Parallel()
	_, err := sqb.ParseSort("evil_column", map[string]string{"name": "u.name"})
	if err == nil {
		t.Fatal("expected error for unknown field")
	}
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != sqb.CodeInvalidSort || e.Kind != errs.KindValidation {
		t.Errorf("err = %+v, want KindValidation/CodeInvalidSort", err)
	}
}

func TestApplySort_AppendsOrderBy(t *testing.T) {
	t.Parallel()
	b := sqb.Builder.Select("id").From("u")
	b = sqb.ApplySort(b, []sqb.SortSpec{
		{Column: "u.name", Order: sqb.SortAsc},
		{Column: "u.id", Order: sqb.SortDesc},
	})
	sql, _, err := b.ToSql()
	if err != nil {
		t.Fatal(err)
	}
	want := "SELECT id FROM u ORDER BY u.name ASC, u.id DESC"
	if sql != want {
		t.Errorf("sql = %q, want %q", sql, want)
	}
}

func TestSort_OneShotWiring(t *testing.T) {
	t.Parallel()
	b, err := sqb.Sort(
		sqb.Builder.Select("id").From("u"),
		"-created_at",
		map[string]string{"created_at": "u.created_at"},
	)
	if err != nil {
		t.Fatal(err)
	}
	sql, _, _ := b.ToSql()
	want := "SELECT id FROM u ORDER BY u.created_at DESC"
	if sql != want {
		t.Errorf("sql = %q, want %q", sql, want)
	}
}

// ── InBatches ─────────────────────────────────────────────────────

func TestInBatches_ChunksEvenly(t *testing.T) {
	t.Parallel()
	items := []int{1, 2, 3, 4, 5, 6, 7}
	var chunks [][]int
	err := sqb.InBatches(items, 3, func(c []int) error {
		chunks = append(chunks, append([]int(nil), c...))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 3 {
		t.Fatalf("got %d chunks, want 3", len(chunks))
	}
	if len(chunks[0]) != 3 || len(chunks[1]) != 3 || len(chunks[2]) != 1 {
		t.Errorf("chunk sizes = %d/%d/%d", len(chunks[0]), len(chunks[1]), len(chunks[2]))
	}
}

func TestInBatches_EmptySliceNoCall(t *testing.T) {
	t.Parallel()
	called := false
	err := sqb.InBatches([]int{}, 5, func([]int) error {
		called = true
		return nil
	})
	if err != nil || called {
		t.Errorf("err=%v called=%v", err, called)
	}
}

func TestInBatches_PropagatesError(t *testing.T) {
	t.Parallel()
	want := errors.New("boom")
	count := 0
	err := sqb.InBatches([]int{1, 2, 3, 4, 5}, 2, func([]int) error {
		count++
		if count == 2 {
			return want
		}
		return nil
	})
	if !errors.Is(err, want) {
		t.Errorf("err = %v, want %v", err, want)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2 (should stop on first err)", count)
	}
}

func TestInBatches_ZeroSizePanics(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on size=0")
		}
	}()
	_ = sqb.InBatches([]int{1}, 0, func([]int) error { return nil })
}

// ── Cursor / CursorPage ───────────────────────────────────────────

func TestCursor_EncodeDecodeRoundTrip(t *testing.T) {
	t.Parallel()
	c := sqb.Cursor{
		CreatedAt: time.Date(2026, 1, 15, 12, 30, 45, 0, time.UTC),
		ID:        "abc-123",
	}
	enc := c.Encode()
	dec, err := sqb.DecodeCursor(enc)
	if err != nil {
		t.Fatal(err)
	}
	if !dec.CreatedAt.Equal(c.CreatedAt) || dec.ID != c.ID {
		t.Errorf("round-trip: got %+v, want %+v", dec, c)
	}
}

func TestDecodeCursor_EmptyIsZero(t *testing.T) {
	t.Parallel()
	c, err := sqb.DecodeCursor("")
	if err != nil {
		t.Fatal(err)
	}
	if !c.CreatedAt.IsZero() || c.ID != "" {
		t.Errorf("expected zero cursor, got %+v", c)
	}
}

func TestDecodeCursor_MalformedErrors(t *testing.T) {
	t.Parallel()
	_, err := sqb.DecodeCursor("not!valid!base64!")
	if err == nil {
		t.Fatal("expected error")
	}
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != sqb.CodeInvalidCursor || e.Kind != errs.KindValidation {
		t.Errorf("err = %+v, want KindValidation/CodeInvalidCursor", err)
	}
}

func TestCursorPage_Apply_LimitOnly(t *testing.T) {
	t.Parallel()
	b, err := sqb.CursorPage{Limit: 10}.Apply(
		sqb.Builder.Select("id").From("u").OrderBy("created_at DESC"),
		"u.created_at", "u.id",
	)
	if err != nil {
		t.Fatal(err)
	}
	sql, _, _ := b.ToSql()
	// No "after" — should only have LIMIT, no WHERE.
	want := "SELECT id FROM u ORDER BY created_at DESC LIMIT 10"
	if sql != want {
		t.Errorf("sql = %q, want %q", sql, want)
	}
}

func TestCursorPage_Apply_WithCursor(t *testing.T) {
	t.Parallel()
	c := sqb.Cursor{
		CreatedAt: time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC),
		ID:        "x-1",
	}
	b, err := sqb.CursorPage{Limit: 5, After: c.Encode()}.Apply(
		sqb.Builder.Select("id").From("u").OrderBy("created_at DESC", "id DESC"),
		"u.created_at", "u.id",
	)
	if err != nil {
		t.Fatal(err)
	}
	sql, args, _ := b.ToSql()
	if !contains(sql, "(u.created_at, u.id) < ($1, $2)") {
		t.Errorf("sql lacks keyset clause: %s", sql)
	}
	if len(args) != 2 {
		t.Errorf("args = %v", args)
	}
}

func TestCursorPage_Apply_DefaultsAndClamp(t *testing.T) {
	t.Parallel()
	// Limit=0 → PageDefaultLimit.
	b1, _ := sqb.CursorPage{}.Apply(
		sqb.Builder.Select("id").From("u"), "u.created_at", "u.id")
	sql1, _, _ := b1.ToSql()
	if !contains(sql1, "LIMIT 20") {
		t.Errorf("default limit not applied: %s", sql1)
	}

	// Limit=9999 → clamped to PageMaxLimit (100).
	b2, _ := sqb.CursorPage{Limit: 9999}.Apply(
		sqb.Builder.Select("id").From("u"), "u.created_at", "u.id")
	sql2, _, _ := b2.ToSql()
	if !contains(sql2, "LIMIT 100") {
		t.Errorf("limit not clamped: %s", sql2)
	}
}

func TestCursorPage_Apply_BadCursorErrors(t *testing.T) {
	t.Parallel()
	_, err := sqb.CursorPage{Limit: 5, After: "!!!"}.Apply(
		sqb.Builder.Select("id").From("u"), "u.created_at", "u.id")
	if err == nil {
		t.Fatal("expected error")
	}
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != sqb.CodeInvalidCursor {
		t.Errorf("err = %+v", err)
	}
}

// silence unused
var _ = sq.Eq{}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
