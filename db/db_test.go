package db_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/errs"
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

func TestConnect_WithLogger_LogsQueries(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	pgOnce.Do(initPostgresContainer)
	if pgErr != nil {
		t.Fatalf("postgres: %v", pgErr)
	}
	d, err := db.Connect(context.Background(), pgCfg, db.WithLogger(logger))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(d.Close)

	if _, err := d.Exec(context.Background(), "SELECT 1"); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if !strings.Contains(buf.String(), "db query") {
		t.Fatalf("expected tracer log, got %q", buf.String())
	}
}

func TestConnect_FailsAfterBudget(t *testing.T) {
	cfg := db.Config{
		Host:               "127.0.0.1",
		Port:               1, // unreachable
		User:               "x",
		Password:           "x",
		Database:           "x",
		SSLMode:            "disable",
		ConnectMaxRetries:  2,
		ConnectBackoffBase: 10 * time.Millisecond,
		ConnectBackoffMax:  20 * time.Millisecond,
	}
	start := time.Now()
	_, err := db.Connect(context.Background(), cfg)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected error")
	}
	// 2 retries with 10ms + 20ms backoff = >=30ms, well under 2s.
	if elapsed > 2*time.Second {
		t.Fatalf("budget exceeded: %v", elapsed)
	}
}

func TestConnect_CtxCancelDuringBackoff(t *testing.T) {
	cfg := db.Config{
		Host:               "127.0.0.1",
		Port:               1,
		User:               "x",
		Password:           "x",
		Database:           "x",
		SSLMode:            "disable",
		ConnectMaxRetries:  100,
		ConnectBackoffBase: 100 * time.Millisecond,
		ConnectBackoffMax:  1 * time.Second,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := db.Connect(ctx, cfg)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected error")
	}
	// The retry loop must abort on ctx cancel within ~one backoff period;
	// must NOT consume the full MaxRetries x backoff_max budget.
	if elapsed > 1*time.Second {
		t.Fatalf("did not abort on ctx cancel: %v", elapsed)
	}
}

func TestConnect_URLOverridesIndividualFields(t *testing.T) {
	pgOnce.Do(initPostgresContainer)
	if pgErr != nil {
		t.Fatalf("postgres: %v", pgErr)
	}
	cfg := db.Config{
		URL:            fmt.Sprintf("postgres://test:test@%s:%d/test?sslmode=disable", pgCfg.Host, pgCfg.Port),
		Host:           "bogus.example.com",
		Port:           1,
		User:           "bogus",
		Password:       "bogus",
		Database:       "bogus",
		SSLMode:        "disable",
		ConnectTimeout: 5 * time.Second,
	}
	d, err := db.Connect(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(d.Close)
	var one int
	if err := d.QueryRow(context.Background(), "SELECT 1").Scan(&one); err != nil {
		t.Fatalf("select 1: %v", err)
	}
	if one != 1 {
		t.Fatalf("got %d, want 1", one)
	}
}

func TestConnect_HasReadReplica_FailsLoudWhenNoStandby(t *testing.T) {
	pgOnce.Do(initPostgresContainer)
	if pgErr != nil {
		t.Fatalf("postgres: %v", pgErr)
	}
	cfg := pgCfg
	cfg.HasReadReplica = true
	cfg.ConnectMaxRetries = 0
	cfg.ConnectTimeout = 2 * time.Second
	_, err := db.Connect(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected error (single-node container has no standby), got nil")
	}
	var e *errs.Error
	if !errors.As(err, &e) || e.Kind != errs.KindUnavailable {
		t.Fatalf("expected *errs.Error{Kind:KindUnavailable}, got %v (%T)", err, err)
	}
	if e.Code != "db_unavailable" {
		t.Fatalf("expected Code=db_unavailable, got %q", e.Code)
	}
}

func TestConnect_AppName_VisibleInPgStatActivity(t *testing.T) {
	pgOnce.Do(initPostgresContainer)
	if pgErr != nil {
		t.Fatalf("postgres: %v", pgErr)
	}
	cfg := pgCfg
	cfg.AppName = "urlshort-test-pod-7"
	d, err := db.Connect(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(d.Close)

	var got string
	if err := d.QueryRow(context.Background(),
		`SELECT application_name FROM pg_stat_activity WHERE pid = pg_backend_pid()`,
	).Scan(&got); err != nil {
		t.Fatalf("query pg_stat_activity: %v", err)
	}
	if got != "urlshort-test-pod-7" {
		t.Fatalf("application_name = %q, want urlshort-test-pod-7", got)
	}
}

func TestReadQuery_FallsBackToPrimary_WhenNoReplica(t *testing.T) {
	d := startTestDB(t)
	rows, err := d.ReadQuery(context.Background(), "SELECT 1")
	if err != nil {
		t.Fatalf("ReadQuery: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("expected one row")
	}
	var n int
	if err := rows.Scan(&n); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if n != 1 {
		t.Fatalf("got %d, want 1", n)
	}
}

func TestReadQueryRow_FallsBackToPrimary_WhenNoReplica(t *testing.T) {
	d := startTestDB(t)
	var n int
	if err := d.ReadQueryRow(context.Background(), "SELECT 42").Scan(&n); err != nil {
		t.Fatalf("ReadQueryRow: %v", err)
	}
	if n != 42 {
		t.Fatalf("got %d, want 42", n)
	}
}

func TestReadPools_EmptyWhenNoReplica(t *testing.T) {
	d := startTestDB(t)
	if got := d.ReadPools(); len(got) != 0 {
		t.Fatalf("expected ReadPools() empty with HasReadReplica=false, got %d entries", len(got))
	}
}
