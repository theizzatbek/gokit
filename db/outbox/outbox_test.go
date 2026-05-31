package outbox_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tcpg "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/db/outbox"
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
		MaxConns:       1,
		MinConns:       1,
	}
}

// freshDB opens a *db.DB against a freshly-created schema so each
// test sees an empty outbox table.
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
	schema := fmt.Sprintf("ob_%d_%d", time.Now().UnixNano(), os.Getpid())
	if _, err := d.Pool().Exec(context.Background(),
		fmt.Sprintf("CREATE SCHEMA %s; SET search_path TO %s", schema, schema)); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	if _, err := d.Exec(context.Background(), outbox.Schema()); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	return d
}

func TestEnqueue_InsertsRow(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()

	err := d.Tx(ctx, func(tx *db.Tx) error {
		return outbox.Enqueue(ctx, tx, outbox.Event{
			AggregateType: "link",
			AggregateID:   "abc",
			EventType:     "test.created",
			Payload:       []byte(`{"x":1}`),
		})
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	var count int
	if err := d.QueryRow(ctx,
		`SELECT count(*) FROM outbox WHERE event_type = $1`, "test.created").Scan(&count); err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
}

func TestEnqueue_MissingEventType_Validation(t *testing.T) {
	d := freshDB(t)
	err := d.Tx(context.Background(), func(tx *db.Tx) error {
		return outbox.Enqueue(context.Background(), tx, outbox.Event{Payload: []byte(`{}`)})
	})
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestEnqueue_OnlyCommitsWithSurroundingTx(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()

	// Tx returns an error → outbox row should NOT be persisted.
	wantErr := errors.New("simulated rollback")
	err := d.Tx(ctx, func(tx *db.Tx) error {
		if err := outbox.Enqueue(ctx, tx, outbox.Event{
			EventType: "test.created",
			Payload:   []byte(`{}`),
		}); err != nil {
			return err
		}
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Tx err = %v, want %v", err, wantErr)
	}

	var count int
	if err := d.QueryRow(ctx, `SELECT count(*) FROM outbox`).Scan(&count); err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 0 {
		t.Errorf("count = %d, want 0 (rollback should have skipped insert)", count)
	}
}

func TestWorker_DrainsEvents(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if err := d.Tx(ctx, func(tx *db.Tx) error {
			return outbox.Enqueue(ctx, tx, outbox.Event{
				EventType: "test.created",
				Payload:   mustJSON(map[string]int{"i": i}),
			})
		}); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
	}

	var seen int32
	w, err := outbox.NewWorker(d, func(_ context.Context, _ outbox.Event) error {
		atomic.AddInt32(&seen, 1)
		return nil
	}, outbox.WithInterval(50*time.Millisecond), outbox.WithBatchSize(10))
	if err != nil {
		t.Fatal(err)
	}
	loopCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := w.Start(loopCtx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Stop() })

	waitFor(t, time.Second, func() bool { return atomic.LoadInt32(&seen) == 3 })

	var unpublished int
	if err := d.QueryRow(ctx,
		`SELECT count(*) FROM outbox WHERE published_at IS NULL`).Scan(&unpublished); err != nil {
		t.Fatalf("query: %v", err)
	}
	if unpublished != 0 {
		t.Errorf("unpublished = %d, want 0", unpublished)
	}
}

func TestWorker_FailedPublishRetries(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()

	if err := d.Tx(ctx, func(tx *db.Tx) error {
		return outbox.Enqueue(ctx, tx, outbox.Event{
			EventType: "test.flaky",
			Payload:   []byte(`{}`),
		})
	}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	var attempts int32
	w, err := outbox.NewWorker(d, func(_ context.Context, _ outbox.Event) error {
		n := atomic.AddInt32(&attempts, 1)
		if n < 3 {
			return errors.New("transient")
		}
		return nil
	}, outbox.WithInterval(50*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	loopCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := w.Start(loopCtx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Stop() })

	waitFor(t, 2*time.Second, func() bool { return atomic.LoadInt32(&attempts) >= 3 })

	var (
		attemptsCol int
		lastError   string
		published   *time.Time
	)
	if err := d.QueryRow(ctx,
		`SELECT attempts, COALESCE(last_error, ''), published_at FROM outbox`).Scan(
		&attemptsCol, &lastError, &published); err != nil {
		t.Fatalf("query: %v", err)
	}
	if attemptsCol < 2 {
		t.Errorf("attempts = %d, want >=2 (transient failures should bump count)", attemptsCol)
	}
	if published == nil {
		t.Error("published_at still NULL — worker should have eventually published")
	}
}

func TestWorker_MaxAttemptsDeadLetters(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()

	if err := d.Tx(ctx, func(tx *db.Tx) error {
		return outbox.Enqueue(ctx, tx, outbox.Event{
			EventType: "test.always-fail",
			Payload:   []byte(`{}`),
		})
	}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	var attempts int32
	w, err := outbox.NewWorker(d, func(_ context.Context, _ outbox.Event) error {
		atomic.AddInt32(&attempts, 1)
		return errors.New("nope")
	},
		outbox.WithInterval(30*time.Millisecond),
		outbox.WithMaxAttempts(2),
	)
	if err != nil {
		t.Fatal(err)
	}
	loopCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := w.Start(loopCtx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Stop() })

	// Wait long enough that worker has retried more than maxAttempts
	// would allow — if dead-letter filter works, attempts caps at 2.
	time.Sleep(500 * time.Millisecond)

	got := atomic.LoadInt32(&attempts)
	if got > 2 {
		t.Errorf("attempts = %d, want <= 2 (max_attempts should filter out the row)", got)
	}
	if got < 2 {
		t.Errorf("attempts = %d, want exactly 2 (worker should have retried twice)", got)
	}
}

func TestWorker_StartTwiceErrors(t *testing.T) {
	d := freshDB(t)
	w, err := outbox.NewWorker(d, func(context.Context, outbox.Event) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := w.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Stop() })
	if err := w.Start(ctx); err == nil {
		t.Error("second Start should error")
	}
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// waitFor polls cond every 25ms until it returns true or timeout
// elapses. Used to bound the worker-loop assertions without coupling
// to the worker's interval.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("condition not met within %v", timeout)
}
