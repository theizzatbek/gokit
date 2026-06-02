package db_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/errs"
)

func TestIsRetryableTxConflict(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"plain", errors.New("boom"), false},
		{"40001", &pgconn.PgError{Code: "40001"}, true},
		{"40P01", &pgconn.PgError{Code: "40P01"}, true},
		{"23505", &pgconn.PgError{Code: "23505"}, false},
		// errs.Wrap should leave errors.As able to reach the inner *PgError.
		{"wrapped 40001",
			errs.Wrap(&pgconn.PgError{Code: "40001"}, errs.KindConflict, "tx_conflict", "wrapped"),
			true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := db.IsRetryableTxConflict(tc.err); got != tc.want {
				t.Errorf("IsRetryableTxConflict(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestTxRetry_RetriesUntilSuccess verifies the classifier-driven retry
// loop without needing a real SERIALIZABLE conflict: we inject a custom
// classifier that treats a sentinel error as retryable, and the fn
// returns that error twice before succeeding.
func TestTxRetry_RetriesUntilSuccess(t *testing.T) {
	d := startTestDB(t)
	setupKV(t, d)

	retryErr := errors.New("fake-retryable")
	var attempts atomic.Int32
	err := d.TxRetry(context.Background(),
		func(tx *db.Tx) error {
			n := attempts.Add(1)
			if n < 3 {
				return retryErr
			}
			_, err := tx.Exec(context.Background(), `INSERT INTO kv VALUES ('ok','1')`)
			return err
		},
		db.WithTxRetryMaxAttempts(5),
		db.WithTxRetryBackoff(1*time.Millisecond, 2*time.Millisecond),
		db.WithTxRetryClassifier(func(e error) bool { return errors.Is(e, retryErr) }),
	)
	if err != nil {
		t.Fatalf("TxRetry: %v", err)
	}
	if attempts.Load() != 3 {
		t.Errorf("attempts = %d, want 3", attempts.Load())
	}
	if got := countKV(t, d); got != 1 {
		t.Errorf("rows = %d, want 1", got)
	}
}

func TestTxRetry_NonRetryableSurfacesImmediately(t *testing.T) {
	d := startTestDB(t)
	setupKV(t, d)

	fatal := errors.New("not-retryable")
	var attempts atomic.Int32
	err := d.TxRetry(context.Background(),
		func(*db.Tx) error {
			attempts.Add(1)
			return fatal
		},
		db.WithTxRetryClassifier(db.IsRetryableTxConflict),
	)
	if !errors.Is(err, fatal) {
		t.Errorf("err = %v, want %v", err, fatal)
	}
	if attempts.Load() != 1 {
		t.Errorf("attempts = %d, want 1 (no retry on non-retryable)", attempts.Load())
	}
}

func TestTxRetry_ExhaustionReturnsLastErr(t *testing.T) {
	d := startTestDB(t)
	setupKV(t, d)

	persist := errors.New("always-retryable-never-succeeds")
	err := d.TxRetry(context.Background(),
		func(*db.Tx) error { return persist },
		db.WithTxRetryMaxAttempts(2),
		db.WithTxRetryBackoff(1*time.Millisecond, 2*time.Millisecond),
		db.WithTxRetryClassifier(func(e error) bool { return errors.Is(e, persist) }),
	)
	if !errors.Is(err, persist) {
		t.Errorf("err = %v, want %v (last retryable err)", err, persist)
	}
}

func TestTxRetry_CtxCancelDuringBackoff(t *testing.T) {
	d := startTestDB(t)
	setupKV(t, d)

	ctx, cancel := context.WithCancel(context.Background())
	retryErr := errors.New("retry-me")
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	err := d.TxRetry(ctx,
		func(*db.Tx) error { return retryErr },
		db.WithTxRetryMaxAttempts(100),
		db.WithTxRetryBackoff(50*time.Millisecond, 50*time.Millisecond),
		db.WithTxRetryClassifier(func(e error) bool { return errors.Is(e, retryErr) }),
	)
	if err == nil || !strings.Contains(err.Error(), "db_unavailable") {
		t.Errorf("err = %v, want db_unavailable on ctx cancel", err)
	}
}

func TestTxRetry_MetricCountsAttempts(t *testing.T) {
	reg := prometheus.NewRegistry()
	d := startTestDB(t, db.WithMetrics(reg))
	setupKV(t, d)

	retryErr := errors.New("retry")
	var attempts atomic.Int32
	_ = d.TxRetry(context.Background(),
		func(*db.Tx) error {
			n := attempts.Add(1)
			if n < 3 {
				return retryErr
			}
			return nil
		},
		db.WithTxRetryMaxAttempts(5),
		db.WithTxRetryBackoff(1*time.Millisecond, 2*time.Millisecond),
		db.WithTxRetryClassifier(func(e error) bool { return errors.Is(e, retryErr) }),
	)

	mfs, _ := reg.Gather()
	var got float64
	for _, mf := range mfs {
		if mf.GetName() == "db_tx_retries_total" {
			got = mf.Metric[0].GetCounter().GetValue()
		}
	}
	// 3 attempts total → 2 retries.
	if got != 2 {
		t.Errorf("db_tx_retries_total = %v, want 2", got)
	}
}

func TestTxWithOpts_ReadOnlyBlocksWrites(t *testing.T) {
	d := startTestDB(t)
	setupKV(t, d)

	err := d.TxWithOpts(context.Background(),
		db.TxOpts{AccessMode: db.ReadOnly},
		func(tx *db.Tx) error {
			_, err := tx.Exec(context.Background(), `INSERT INTO kv VALUES ('x','1')`)
			return err
		})
	if err == nil {
		t.Fatal("expected ReadOnly tx to reject INSERT")
	}
}

func TestTxWithOpts_SerializableAppliesIsolation(t *testing.T) {
	d := startTestDB(t)
	setupKV(t, d)

	var iso string
	if err := d.TxWithOpts(context.Background(),
		db.TxOpts{IsoLevel: db.Serializable},
		func(tx *db.Tx) error {
			return tx.QueryRow(context.Background(),
				`SHOW transaction_isolation`).Scan(&iso)
		}); err != nil {
		t.Fatalf("Tx: %v", err)
	}
	if iso != "serializable" {
		t.Errorf("transaction_isolation = %q, want %q", iso, "serializable")
	}
}
