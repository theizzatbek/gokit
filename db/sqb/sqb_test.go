package sqb_test

import (
	"context"
	"testing"

	sq "github.com/Masterminds/squirrel"

	"github.com/theizzatbek/fibermap/db"
	"github.com/theizzatbek/fibermap/db/sqb"
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
