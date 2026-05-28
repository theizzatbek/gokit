package sqb_test

import (
	"context"
	"testing"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"

	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/db/sqb"
)

func newDB(t *testing.T) *db.DB { return startTestSqbDB(t) }

func TestSqb_Query_RoundTrip(t *testing.T) {
	d := newDB(t)
	ctx := context.Background()

	if _, err := d.Exec(ctx, `CREATE TABLE u (id int PRIMARY KEY, name text)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := d.Exec(ctx, `INSERT INTO u VALUES (1,'a'),(2,'b')`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	rows, err := sqb.Query(ctx, d,
		sqb.Builder.Select("name").From("u").Where(sq.Eq{"id": 1}))
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("no rows")
	}
	var name string
	if err := rows.Scan(&name); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if name != "a" {
		t.Fatalf("name = %q, want a", name)
	}
}

type uRow struct {
	ID   int
	Name string
}

func scanU(r pgx.Row, dst *uRow) error {
	return r.Scan(&dst.ID, &dst.Name)
}

func TestSqb_QueryAll_ReturnsAllRows(t *testing.T) {
	d := newDB(t)
	ctx := context.Background()
	if _, err := d.Exec(ctx, `CREATE TABLE u (id int PRIMARY KEY, name text)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := d.Exec(ctx, `INSERT INTO u VALUES (1,'a'),(2,'b'),(3,'c')`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	got, err := sqb.QueryAll[uRow](ctx, d,
		sqb.Builder.Select("id", "name").From("u").OrderBy("id"), scanU)
	if err != nil {
		t.Fatalf("QueryAll: %v", err)
	}
	want := []uRow{{1, "a"}, {2, "b"}, {3, "c"}}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d (got %v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestSqb_QueryAll_EmptyResultIsNilSlice(t *testing.T) {
	d := newDB(t)
	ctx := context.Background()
	if _, err := d.Exec(ctx, `CREATE TABLE u (id int PRIMARY KEY, name text)`); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := sqb.QueryAll[uRow](ctx, d,
		sqb.Builder.Select("id", "name").From("u"), scanU)
	if err != nil {
		t.Fatalf("QueryAll: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d rows from empty table", len(got))
	}
}

func TestSqb_QueryAll_PaginationRoundTrip(t *testing.T) {
	d := newDB(t)
	ctx := context.Background()
	if _, err := d.Exec(ctx, `CREATE TABLE u (id int PRIMARY KEY, name text)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	for i := 1; i <= 5; i++ {
		if _, err := d.Exec(ctx, `INSERT INTO u VALUES ($1, $2)`, i, "n"); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	got, err := sqb.QueryAll[uRow](ctx, d,
		sqb.Page{Limit: 2, Offset: 1}.Apply(
			sqb.Builder.Select("id", "name").From("u").OrderBy("id")), scanU)
	if err != nil {
		t.Fatalf("QueryAll: %v", err)
	}
	if len(got) != 2 || got[0].ID != 2 || got[1].ID != 3 {
		t.Errorf("pagination misbehaved: got %v", got)
	}
}

func TestSqb_QueryOne_RoundTrip(t *testing.T) {
	d := newDB(t)
	ctx := context.Background()
	if _, err := d.Exec(ctx, `CREATE TABLE u (id int PRIMARY KEY, name text)`); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := sqb.QueryOne[uRow](ctx, d,
		sqb.Builder.Insert("u").Columns("id", "name").Values(7, "z").
			Suffix("RETURNING id, name"), scanU)
	if err != nil {
		t.Fatalf("QueryOne: %v", err)
	}
	if got.ID != 7 || got.Name != "z" {
		t.Errorf("got %v, want {7 z}", got)
	}
}

func TestSqb_Exec_InsertUpdateDelete(t *testing.T) {
	d := newDB(t)
	ctx := context.Background()
	if _, err := d.Exec(ctx, `CREATE TABLE u (id int PRIMARY KEY, name text)`); err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := sqb.Exec(ctx, d, sqb.Builder.Insert("u").Columns("id", "name").Values(1, "x")); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if _, err := sqb.Exec(ctx, d, sqb.Builder.Update("u").Set("name", "y").Where(sq.Eq{"id": 1})); err != nil {
		t.Fatalf("update: %v", err)
	}
	if _, err := sqb.Exec(ctx, d, sqb.Builder.Delete("u").Where(sq.Eq{"id": 1})); err != nil {
		t.Fatalf("delete: %v", err)
	}

	var n int
	if err := d.QueryRow(ctx, `SELECT count(*) FROM u`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("count = %d, want 0", n)
	}
}
