package db_test

import (
	"context"
	"errors"
	"testing"

	"github.com/theizzatbek/fibermap/db"
)

func setupKV(t *testing.T, d *db.DB) {
	t.Helper()
	if _, err := d.Exec(context.Background(),
		`CREATE TABLE kv (k text PRIMARY KEY, v text NOT NULL)`); err != nil {
		t.Fatalf("create: %v", err)
	}
}

func countKV(t *testing.T, d *db.DB) int {
	t.Helper()
	var n int
	if err := d.QueryRow(context.Background(), `SELECT count(*) FROM kv`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func TestTx_Commit(t *testing.T) {
	d := startTestDB(t)
	setupKV(t, d)
	err := d.Tx(context.Background(), func(tx *db.Tx) error {
		_, err := tx.Exec(context.Background(), `INSERT INTO kv VALUES ('a','1')`)
		return err
	})
	if err != nil {
		t.Fatalf("Tx: %v", err)
	}
	if got := countKV(t, d); got != 1 {
		t.Fatalf("count = %d, want 1", got)
	}
}

func TestTx_RollbackOnError(t *testing.T) {
	d := startTestDB(t)
	setupKV(t, d)
	want := errors.New("forced")
	err := d.Tx(context.Background(), func(tx *db.Tx) error {
		if _, err := tx.Exec(context.Background(), `INSERT INTO kv VALUES ('a','1')`); err != nil {
			return err
		}
		return want
	})
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
	if got := countKV(t, d); got != 0 {
		t.Fatalf("count = %d, want 0 (rolled back)", got)
	}
}

func TestTx_RollbackOnPanic(t *testing.T) {
	d := startTestDB(t)
	setupKV(t, d)
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic to propagate")
		}
		if got := countKV(t, d); got != 0 {
			t.Fatalf("count = %d, want 0", got)
		}
	}()
	_ = d.Tx(context.Background(), func(tx *db.Tx) error {
		_, _ = tx.Exec(context.Background(), `INSERT INTO kv VALUES ('a','1')`)
		panic("boom")
	})
}

func TestTx_SatisfiesQuerier(t *testing.T) {
	d := startTestDB(t)
	setupKV(t, d)
	_ = d.Tx(context.Background(), func(tx *db.Tx) error {
		var q db.Querier = tx
		_, err := q.Exec(context.Background(), `INSERT INTO kv VALUES ('x','1')`)
		return err
	})
	if got := countKV(t, d); got != 1 {
		t.Fatalf("count = %d, want 1", got)
	}
}
