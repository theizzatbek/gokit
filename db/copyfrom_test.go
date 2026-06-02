package db_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/theizzatbek/gokit/db"
)

func TestCopyFrom_BulkInsert(t *testing.T) {
	d := startTestDB(t)
	setupKV(t, d)

	rows := [][]any{
		{"a", "1"}, {"b", "2"}, {"c", "3"},
	}
	n, err := d.CopyFrom(context.Background(),
		pgx.Identifier{"kv"},
		[]string{"k", "v"},
		pgx.CopyFromRows(rows))
	if err != nil {
		t.Fatalf("CopyFrom: %v", err)
	}
	if n != int64(len(rows)) {
		t.Errorf("rowsAffected = %d, want %d", n, len(rows))
	}
	if got := countKV(t, d); got != 3 {
		t.Errorf("count = %d, want 3", got)
	}
}

func TestCopyFrom_InTx_AtomicWithSurroundingExec(t *testing.T) {
	d := startTestDB(t)
	setupKV(t, d)

	want := []byte("rolling back")
	_ = d.Tx(context.Background(), func(tx *db.Tx) error {
		if _, err := tx.CopyFrom(context.Background(),
			pgx.Identifier{"kv"},
			[]string{"k", "v"},
			pgx.CopyFromRows([][]any{{"x", "1"}})); err != nil {
			t.Fatalf("CopyFrom in tx: %v", err)
		}
		// Force rollback so CopyFrom's rows must vanish.
		return &simpleErr{msg: string(want)}
	})
	if got := countKV(t, d); got != 0 {
		t.Errorf("count = %d, want 0 (rolled back with surrounding tx)", got)
	}
}

type simpleErr struct{ msg string }

func (e *simpleErr) Error() string { return e.msg }
