package db

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/theizzatbek/gokit/errs"
)

func TestBulkUpdate_BuildSQL(t *testing.T) {
	bu := NewBulkUpdate("users", "id").
		Columns("email", "display_name").
		Add("u-1", "a@b.com", "Alice").
		Add("u-2", "b@c.com", "Bob")

	sql, args := bu.buildSQL()
	want := `UPDATE users SET email = t.email, display_name = t.display_name FROM (VALUES ($1, $2, $3), ($4, $5, $6)) AS t(id, email, display_name) WHERE users.id = t.id`
	if sql != want {
		t.Errorf("sql =\n  %q\nwant:\n  %q", sql, want)
	}
	wantArgs := []any{"u-1", "a@b.com", "Alice", "u-2", "b@c.com", "Bob"}
	if len(args) != len(wantArgs) {
		t.Fatalf("len(args) = %d, want %d", len(args), len(wantArgs))
	}
	for i, want := range wantArgs {
		if args[i] != want {
			t.Errorf("args[%d] = %v, want %v", i, args[i], want)
		}
	}
}

func TestBulkUpdate_Len(t *testing.T) {
	bu := NewBulkUpdate("t", "id").Columns("v")
	if bu.Len() != 0 {
		t.Errorf("empty Len = %d, want 0", bu.Len())
	}
	bu.Add(1, "a")
	bu.Add(2, "b")
	if bu.Len() != 2 {
		t.Errorf("Len = %d, want 2", bu.Len())
	}
}

func TestBulkUpdate_Exec_EmptyIsNoOp(t *testing.T) {
	bu := NewBulkUpdate("users", "id").Columns("email")
	q := &stubQuerier{}
	n, err := bu.Exec(context.Background(), q)
	if err != nil {
		t.Errorf("err = %v, want nil", err)
	}
	if n != 0 {
		t.Errorf("rows = %d, want 0", n)
	}
	if q.gotSQL != "" {
		t.Errorf("DB was touched on empty bulk: sql=%q", q.gotSQL)
	}
}

func TestBulkUpdate_Exec_ValidationFailures(t *testing.T) {
	q := &stubQuerier{}
	cases := []struct {
		name string
		bu   *BulkUpdate
		code string
	}{
		{
			name: "no table",
			bu:   NewBulkUpdate("", "id").Columns("v").Add(1, "a"),
			code: "db_bulk_no_table",
		},
		{
			name: "no key",
			bu:   NewBulkUpdate("t", "").Columns("v").Add(1, "a"),
			code: "db_bulk_no_key",
		},
		{
			name: "no columns",
			bu:   NewBulkUpdate("t", "id").Add(1),
			code: "db_bulk_no_columns",
		},
		{
			name: "row arity",
			bu:   NewBulkUpdate("t", "id").Columns("a", "b").Add(1, "only-a"),
			code: "db_bulk_row_arity",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.bu.Exec(context.Background(), q)
			var e *errs.Error
			if !errors.As(err, &e) || e.Code != tc.code {
				t.Errorf("err = %v, want code %q", err, tc.code)
			}
		})
	}
}

func TestBulkUpdate_Exec_NilQuerier(t *testing.T) {
	bu := NewBulkUpdate("t", "id").Columns("v").Add(1, "a")
	_, err := bu.Exec(context.Background(), nil)
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != "db_nil_querier" {
		t.Errorf("err = %v, want db_nil_querier", err)
	}
}

func TestBulkUpdate_BuildSQL_ContainsAllPlaceholders(t *testing.T) {
	bu := NewBulkUpdate("t", "id").
		Columns("a", "b", "c").
		Add(1, "x", "y", "z").
		Add(2, "p", "q", "r")
	sql, _ := bu.buildSQL()
	// 4 columns × 2 rows = 8 placeholders, $1..$8.
	for i := 1; i <= 8; i++ {
		want := "$" + itoa(i)
		if !strings.Contains(sql, want) {
			t.Errorf("missing placeholder %s in %q", want, sql)
		}
	}
}

func itoa(i int) string {
	const digits = "0123456789"
	if i == 0 {
		return "0"
	}
	out := ""
	for i > 0 {
		out = string(digits[i%10]) + out
		i /= 10
	}
	return out
}
