package db_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/theizzatbek/gokit/db"
)

func TestWithDefaultStatementTimeout_AppliesPerConnection(t *testing.T) {
	d := startTestDB(t, db.WithDefaultStatementTimeout(750*time.Millisecond))

	var got string
	if err := d.QueryRow(context.Background(),
		`SHOW statement_timeout`).Scan(&got); err != nil {
		t.Fatalf("SHOW statement_timeout: %v", err)
	}
	// Postgres formats this as "750ms" for 750 millis.
	if got != "750ms" {
		t.Errorf("statement_timeout = %q, want %q", got, "750ms")
	}
}

func TestWithConnInit_RunsOncePerConnection(t *testing.T) {
	var called atomic.Int32
	d := startTestDB(t,
		db.WithConnInit(func(ctx context.Context, conn *pgx.Conn) error {
			called.Add(1)
			_, err := conn.Exec(ctx, `SET application_name = 'gokit-test'`)
			return err
		}),
	)
	if called.Load() < 1 {
		t.Fatalf("WithConnInit hook never ran")
	}
	var name string
	if err := d.QueryRow(context.Background(),
		`SHOW application_name`).Scan(&name); err != nil {
		t.Fatalf("SHOW application_name: %v", err)
	}
	if name != "gokit-test" {
		t.Errorf("application_name = %q, want %q", name, "gokit-test")
	}
}

func TestWithConnInit_MultipleHooksAccumulate(t *testing.T) {
	var hits1, hits2 atomic.Int32
	d := startTestDB(t,
		db.WithConnInit(func(ctx context.Context, _ *pgx.Conn) error {
			hits1.Add(1)
			return nil
		}),
		db.WithConnInit(func(ctx context.Context, _ *pgx.Conn) error {
			hits2.Add(1)
			return nil
		}),
	)
	// Trigger a query so we know the connection initialised.
	if _, err := d.Exec(context.Background(), `SELECT 1`); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if hits1.Load() < 1 || hits2.Load() < 1 {
		t.Errorf("hooks counts = %d / %d, want both >= 1", hits1.Load(), hits2.Load())
	}
}
