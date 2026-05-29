package db_test

import (
	"context"
	"errors"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/theizzatbek/gokit/db"
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

func TestTx_NestedSavepoint_Commit(t *testing.T) {
	d := startTestDB(t)
	setupKV(t, d)
	err := d.Tx(context.Background(), func(outer *db.Tx) error {
		if _, err := outer.Exec(context.Background(), `INSERT INTO kv VALUES ('outer','1')`); err != nil {
			return err
		}
		return outer.Tx(context.Background(), func(inner *db.Tx) error {
			_, err := inner.Exec(context.Background(), `INSERT INTO kv VALUES ('inner','2')`)
			return err
		})
	})
	if err != nil {
		t.Fatalf("Tx: %v", err)
	}
	if got := countKV(t, d); got != 2 {
		t.Fatalf("count = %d, want 2", got)
	}
}

// txMetricCount reads db_tx_total{kind,outcome} from a registry into
// which db.WithMetrics has registered. We can't access the unexported
// collector directly from the external _test package, so we walk the
// gathered families instead.
func txMetricCount(t *testing.T, reg *prometheus.Registry, kind, outcome string) float64 {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() != "db_tx_total" {
			continue
		}
		for _, m := range mf.Metric {
			var k, o string
			for _, l := range m.GetLabel() {
				switch l.GetName() {
				case "kind":
					k = l.GetValue()
				case "outcome":
					o = l.GetValue()
				}
			}
			if k == kind && o == outcome {
				return m.GetCounter().GetValue()
			}
		}
	}
	return 0
}

func TestTx_Metrics_CommitRollbackPanic(t *testing.T) {
	reg := prometheus.NewRegistry()
	d := startTestDB(t, db.WithMetrics(reg))
	setupKV(t, d)

	// commit
	if err := d.Tx(context.Background(), func(tx *db.Tx) error {
		_, err := tx.Exec(context.Background(), `INSERT INTO kv VALUES ('a','1')`)
		return err
	}); err != nil {
		t.Fatalf("commit Tx: %v", err)
	}
	// rollback (returned error)
	want := errors.New("boom")
	if err := d.Tx(context.Background(), func(tx *db.Tx) error {
		_, _ = tx.Exec(context.Background(), `INSERT INTO kv VALUES ('b','2')`)
		return want
	}); !errors.Is(err, want) {
		t.Fatalf("rollback Tx err = %v, want %v", err, want)
	}
	// panic (rolled back AND re-thrown)
	func() {
		defer func() { _ = recover() }()
		_ = d.Tx(context.Background(), func(tx *db.Tx) error {
			panic("kaboom")
		})
	}()

	if v := txMetricCount(t, reg, "tx", "commit"); v != 1 {
		t.Errorf("tx commit count = %v, want 1", v)
	}
	if v := txMetricCount(t, reg, "tx", "rollback"); v != 1 {
		t.Errorf("tx rollback count = %v, want 1", v)
	}
	if v := txMetricCount(t, reg, "tx", "panic"); v != 1 {
		t.Errorf("tx panic count = %v, want 1", v)
	}
}

func TestTx_Metrics_NestedSavepoint_RollbackInner(t *testing.T) {
	reg := prometheus.NewRegistry()
	d := startTestDB(t, db.WithMetrics(reg))
	setupKV(t, d)

	want := errors.New("inner-fail")
	if err := d.Tx(context.Background(), func(outer *db.Tx) error {
		_, _ = outer.Exec(context.Background(), `INSERT INTO kv VALUES ('outer','1')`)
		innerErr := outer.Tx(context.Background(), func(inner *db.Tx) error {
			return want
		})
		if !errors.Is(innerErr, want) {
			t.Fatalf("inner err = %v, want %v", innerErr, want)
		}
		return nil
	}); err != nil {
		t.Fatalf("outer Tx: %v", err)
	}

	if v := txMetricCount(t, reg, "tx", "commit"); v != 1 {
		t.Errorf("outer tx commit = %v, want 1", v)
	}
	if v := txMetricCount(t, reg, "savepoint", "rollback"); v != 1 {
		t.Errorf("savepoint rollback = %v, want 1", v)
	}
	// duration histogram should have observed both
	if got := testutil.CollectAndCount(reg); got == 0 {
		t.Fatal("registry empty — no observations")
	}
}

func TestTx_NestedSavepoint_RollbackInnerKeepsOuter(t *testing.T) {
	d := startTestDB(t)
	setupKV(t, d)
	want := errors.New("inner-fail")
	err := d.Tx(context.Background(), func(outer *db.Tx) error {
		if _, err := outer.Exec(context.Background(), `INSERT INTO kv VALUES ('outer','1')`); err != nil {
			return err
		}
		innerErr := outer.Tx(context.Background(), func(inner *db.Tx) error {
			if _, err := inner.Exec(context.Background(), `INSERT INTO kv VALUES ('inner','2')`); err != nil {
				return err
			}
			return want
		})
		if !errors.Is(innerErr, want) {
			t.Fatalf("inner err = %v, want %v", innerErr, want)
		}
		return nil // outer still commits
	})
	if err != nil {
		t.Fatalf("outer Tx: %v", err)
	}
	if got := countKV(t, d); got != 1 {
		t.Fatalf("count = %d, want 1 (only outer persisted)", got)
	}
}
