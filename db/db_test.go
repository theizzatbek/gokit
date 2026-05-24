package db_test

import (
	"context"
	"errors"
	"testing"

	"github.com/theizzatbek/fibermap/db"
	"github.com/theizzatbek/fibermap/errs"
)

func TestConnect_BadCredentials_KindUnavailable(t *testing.T) {
	startTestDB(t) // ensure container is up; we then point at it with bad creds
	cfg := pgCfg
	cfg.Password = "wrong-password"
	_, err := db.Connect(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var e *errs.Error
	if !errors.As(err, &e) || e.Kind != errs.KindUnavailable {
		t.Fatalf("expected KindUnavailable, got %v / %T", e, err)
	}
}

func TestConnect_Success_PoolReturnsHandle(t *testing.T) {
	d := startTestDB(t)
	if d.Pool() == nil {
		t.Fatal("Pool() returned nil")
	}
}

func TestClose_Idempotent(t *testing.T) {
	d := startTestDB(t)
	d.Close()
	d.Close() // must not panic
}

func TestDB_ExecQueryQueryRow_HappyPath(t *testing.T) {
	d := startTestDB(t)
	ctx := context.Background()

	if _, err := d.Exec(ctx, `CREATE TABLE items (id int PRIMARY KEY, name text NOT NULL)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := d.Exec(ctx, `INSERT INTO items (id, name) VALUES ($1, $2), ($3, $4)`, 1, "a", 2, "b"); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var name string
	if err := d.QueryRow(ctx, `SELECT name FROM items WHERE id = $1`, 1).Scan(&name); err != nil {
		t.Fatalf("queryrow: %v", err)
	}
	if name != "a" {
		t.Fatalf("got %q want a", name)
	}

	rows, err := d.Query(ctx, `SELECT id FROM items ORDER BY id`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		ids = append(ids, id)
	}
	if got := len(ids); got != 2 || ids[0] != 1 || ids[1] != 2 {
		t.Fatalf("ids = %v, want [1 2]", ids)
	}
}

func TestDB_QueryRow_ErrNoRows_KindNotFound(t *testing.T) {
	d := startTestDB(t)
	ctx := context.Background()
	if _, err := d.Exec(ctx, `CREATE TABLE empty_t (id int)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	var x int
	err := d.QueryRow(ctx, `SELECT id FROM empty_t WHERE id = $1`, 99).Scan(&x)
	var e *errs.Error
	if !errors.As(err, &e) || e.Kind != errs.KindNotFound {
		t.Fatalf("want KindNotFound, got %v (%T)", e, err)
	}
}
