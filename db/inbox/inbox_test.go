package inbox_test

import (
	"context"
	"errors"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tcpg "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/db/inbox"
	xerrs "github.com/theizzatbek/gokit/errs"
)

var (
	pgOnce sync.Once
	pgCfg  db.Config
	pgErr  error
)

func TestMain(m *testing.M) { os.Exit(m.Run()) }

func initPostgresContainer() {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	c, err := tcpg.Run(ctx, "postgres:16-alpine",
		tcpg.WithDatabase("test"),
		tcpg.WithUsername("test"),
		tcpg.WithPassword("test"),
		tcpg.BasicWaitStrategies(),
	)
	if err != nil {
		pgErr = err
		return
	}
	host, err := c.Host(ctx)
	if err != nil {
		pgErr = err
		return
	}
	port, err := c.MappedPort(ctx, "5432/tcp")
	if err != nil {
		pgErr = err
		return
	}
	p, _ := strconv.Atoi(port.Port())
	pgCfg = db.Config{
		Host:           host,
		Port:           p,
		User:           "test",
		Password:       "test",
		Database:       "test",
		SSLMode:        "disable",
		ConnectTimeout: 5 * time.Second,
		MaxConns:       8,
		MinConns:       2,
	}
}

func freshDB(t *testing.T) *db.DB {
	t.Helper()
	if testing.Short() {
		t.Skip("requires Postgres testcontainer; rerun without -short")
	}
	pgOnce.Do(initPostgresContainer)
	if pgErr != nil {
		t.Fatalf("postgres: %v", pgErr)
	}
	d, err := db.Connect(context.Background(), pgCfg)
	if err != nil {
		t.Fatalf("db.Connect: %v", err)
	}
	t.Cleanup(d.Close)
	if _, err := d.Exec(context.Background(), inbox.Schema()); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	if _, err := d.Exec(context.Background(), "TRUNCATE TABLE inbox"); err != nil {
		t.Fatalf("truncate inbox: %v", err)
	}
	return d
}

func TestProcess_FirstCallRunsFnReturnsProcessed(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()

	called := false
	outcome, err := inbox.Process(ctx, d, inbox.Key{
		Consumer: "svc:test",
		EventID:  "evt-1",
	}, func(tx *db.Tx) error {
		called = true
		_, err := tx.Exec(ctx, "CREATE TABLE IF NOT EXISTS testdata (id text PRIMARY KEY)")
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, "INSERT INTO testdata VALUES ($1)", "v1")
		return err
	})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if outcome != inbox.OutcomeProcessed {
		t.Errorf("outcome = %v, want Processed", outcome)
	}
	if !called {
		t.Error("fn was not called on first delivery")
	}

	// Verify both the inbox row AND the domain row committed.
	var n int
	if err := d.QueryRow(ctx,
		`SELECT count(*) FROM inbox WHERE consumer = $1 AND event_id = $2`,
		"svc:test", "evt-1").Scan(&n); err != nil {
		t.Fatalf("count inbox: %v", err)
	}
	if n != 1 {
		t.Errorf("inbox rows for key = %d, want 1", n)
	}
	if err := d.QueryRow(ctx, `SELECT count(*) FROM testdata WHERE id = 'v1'`).Scan(&n); err != nil {
		t.Fatalf("count testdata: %v", err)
	}
	if n != 1 {
		t.Errorf("testdata rows = %d, want 1 (domain Tx did not commit)", n)
	}
}

func TestProcess_DuplicateReturnsDuplicateWithoutRunningFn(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()
	key := inbox.Key{Consumer: "svc:test", EventID: "evt-1"}

	// First delivery.
	if _, err := inbox.Process(ctx, d, key, func(*db.Tx) error { return nil }); err != nil {
		t.Fatalf("first Process: %v", err)
	}

	// Second delivery: fn must not run.
	called := false
	outcome, err := inbox.Process(ctx, d, key, func(*db.Tx) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("second Process: %v", err)
	}
	if outcome != inbox.OutcomeDuplicate {
		t.Errorf("outcome = %v, want Duplicate", outcome)
	}
	if called {
		t.Error("fn was called on duplicate delivery — exactly-once contract violated")
	}
}

func TestProcess_FnErrorRollsBackInboxRow(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()
	key := inbox.Key{Consumer: "svc:test", EventID: "evt-1"}

	boom := errors.New("domain failure")
	_, err := inbox.Process(ctx, d, key, func(*db.Tx) error { return boom })
	if err == nil {
		t.Fatal("expected error from fn")
	}
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want wrapping boom", err)
	}

	// Row MUST NOT exist — a future redelivery should run fn again.
	var n int
	if err := d.QueryRow(ctx,
		`SELECT count(*) FROM inbox WHERE consumer = $1 AND event_id = $2`,
		key.Consumer, key.EventID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("inbox row exists after fn error: %d (must be 0)", n)
	}

	// Retry succeeds.
	outcome, err := inbox.Process(ctx, d, key, func(*db.Tx) error { return nil })
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if outcome != inbox.OutcomeProcessed {
		t.Errorf("retry outcome = %v, want Processed", outcome)
	}
}

func TestProcess_MultiConsumerNamespacesAreIndependent(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()
	const eventID = "evt-1"

	o1, err := inbox.Process(ctx, d,
		inbox.Key{Consumer: "svc:a", EventID: eventID},
		func(*db.Tx) error { return nil })
	if err != nil || o1 != inbox.OutcomeProcessed {
		t.Fatalf("consumer A: o=%v err=%v", o1, err)
	}

	// Same EventID under a different consumer must be a fresh row.
	o2, err := inbox.Process(ctx, d,
		inbox.Key{Consumer: "svc:b", EventID: eventID},
		func(*db.Tx) error { return nil })
	if err != nil || o2 != inbox.OutcomeProcessed {
		t.Errorf("consumer B: o=%v err=%v", o2, err)
	}
}

func TestProcess_MissingKeyFieldsValidateFast(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()

	_, err := inbox.Process(ctx, d, inbox.Key{EventID: "x"}, nil)
	if err == nil {
		t.Fatal("expected error for missing Consumer")
	}
	var xe *xerrs.Error
	if !errors.As(err, &xe) || xe.Code != inbox.CodeMissingConsumer {
		t.Errorf("err = %v, want CodeMissingConsumer", err)
	}

	_, err = inbox.Process(ctx, d, inbox.Key{Consumer: "x"}, nil)
	if err == nil {
		t.Fatal("expected error for missing EventID")
	}
	if !errors.As(err, &xe) || xe.Code != inbox.CodeMissingEventID {
		t.Errorf("err = %v, want CodeMissingEventID", err)
	}
}

func TestProcess_RaceExactlyOneProcessed(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()
	key := inbox.Key{Consumer: "svc:race", EventID: "evt-1"}

	const goroutines = 50
	var processed, duplicate, fnCalls atomic.Int64
	start := make(chan struct{})

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			outcome, err := inbox.Process(ctx, d, key, func(*db.Tx) error {
				fnCalls.Add(1)
				return nil
			})
			if err != nil {
				t.Errorf("Process: %v", err)
				return
			}
			switch outcome {
			case inbox.OutcomeProcessed:
				processed.Add(1)
			case inbox.OutcomeDuplicate:
				duplicate.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()

	if processed.Load() != 1 {
		t.Errorf("processed = %d, want exactly 1", processed.Load())
	}
	if duplicate.Load() != int64(goroutines)-1 {
		t.Errorf("duplicate = %d, want %d", duplicate.Load(), goroutines-1)
	}
	if fnCalls.Load() != 1 {
		t.Errorf("fnCalls = %d, want exactly 1", fnCalls.Load())
	}
}

func TestRetention_DeletesOldRows(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()

	// Seed two rows: one with a backdated processed_at, one recent.
	if _, err := d.Exec(ctx,
		`INSERT INTO inbox (consumer, event_id, processed_at) VALUES
		 ('svc', 'old', NOW() - INTERVAL '2 hours'),
		 ('svc', 'new', NOW())`,
	); err != nil {
		t.Fatalf("seed: %v", err)
	}

	w, err := inbox.NewRetentionWorker(d, inbox.RetentionConfig{
		TTL:      time.Hour,
		Interval: time.Hour,
	})
	if err != nil {
		t.Fatalf("NewRetentionWorker: %v", err)
	}

	deleted, err := w.Tick(ctx)
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted = %d, want 1 (only the 2h-old row)", deleted)
	}

	var n int
	if err := d.QueryRow(ctx, `SELECT count(*) FROM inbox`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("remaining rows = %d, want 1", n)
	}
}

func TestRetention_ValidateRejectsZero(t *testing.T) {
	t.Parallel()
	_, err := inbox.NewRetentionWorker(nil, inbox.RetentionConfig{Interval: time.Hour})
	if err == nil {
		t.Fatal("expected validation error for TTL=0")
	}
	var xe *xerrs.Error
	if !errors.As(err, &xe) || xe.Code != inbox.CodeInvalidRetentionTTL {
		t.Errorf("err = %v, want CodeInvalidRetentionTTL", err)
	}

	_, err = inbox.NewRetentionWorker(nil, inbox.RetentionConfig{TTL: time.Hour})
	if err == nil {
		t.Fatal("expected validation error for Interval=0")
	}
	if !errors.As(err, &xe) || xe.Code != inbox.CodeInvalidRetentionInterval {
		t.Errorf("err = %v, want CodeInvalidRetentionInterval", err)
	}
}
