package sqb_test

import (
	"strings"
	"testing"

	"github.com/theizzatbek/gokit/db/sqb"
)

func TestPage_Apply_ZeroLimitDefaults(t *testing.T) {
	b := sqb.Builder.Select("id").From("t")
	b = sqb.Page{}.Apply(b) // both zero
	sql, args, err := b.ToSql()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sql, "LIMIT 20") {
		t.Errorf("expected default LIMIT 20, got %q", sql)
	}
	if !strings.Contains(sql, "OFFSET 0") {
		t.Errorf("expected OFFSET 0, got %q", sql)
	}
	if len(args) != 0 {
		t.Errorf("expected no args, got %v", args)
	}
}

func TestPage_Apply_LimitCappedToMax(t *testing.T) {
	b := sqb.Builder.Select("id").From("t")
	b = sqb.Page{Limit: 10000}.Apply(b)
	sql, _, _ := b.ToSql()
	if !strings.Contains(sql, "LIMIT 100") {
		t.Errorf("expected LIMIT capped to 100, got %q", sql)
	}
}

func TestPage_Apply_NegativeLimitDefaults(t *testing.T) {
	b := sqb.Builder.Select("id").From("t")
	b = sqb.Page{Limit: -5}.Apply(b)
	sql, _, _ := b.ToSql()
	if !strings.Contains(sql, "LIMIT 20") {
		t.Errorf("expected default LIMIT 20 for negative, got %q", sql)
	}
}

func TestPage_Apply_NegativeOffsetClamped(t *testing.T) {
	b := sqb.Builder.Select("id").From("t")
	b = sqb.Page{Limit: 50, Offset: -100}.Apply(b)
	sql, _, _ := b.ToSql()
	if !strings.Contains(sql, "OFFSET 0") {
		t.Errorf("expected OFFSET 0 for negative, got %q", sql)
	}
}

func TestPage_Apply_HonoursValidValues(t *testing.T) {
	b := sqb.Builder.Select("id").From("t").OrderBy("created_at DESC")
	b = sqb.Page{Limit: 50, Offset: 100}.Apply(b)
	sql, _, _ := b.ToSql()
	want := "SELECT id FROM t ORDER BY created_at DESC LIMIT 50 OFFSET 100"
	if sql != want {
		t.Errorf("sql = %q\nwant %q", sql, want)
	}
}

func TestPageConstants_PublicAndStable(t *testing.T) {
	if sqb.PageDefaultLimit != 20 {
		t.Errorf("PageDefaultLimit = %d, want 20", sqb.PageDefaultLimit)
	}
	if sqb.PageMaxLimit != 100 {
		t.Errorf("PageMaxLimit = %d, want 100", sqb.PageMaxLimit)
	}
}
