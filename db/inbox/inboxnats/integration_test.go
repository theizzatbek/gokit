package inboxnats_test

import (
	"context"
	"errors"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	tcpg "github.com/testcontainers/testcontainers-go/modules/postgres"

	natsclient "github.com/theizzatbek/gokit/clients/nats"
	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/db/inbox"
	"github.com/theizzatbek/gokit/db/inbox/inboxnats"
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
		MaxConns:       4,
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
	// Drop+recreate the test-specific domain table so per-test PK
	// inserts do not bleed between tests sharing the container.
	if _, err := d.Exec(context.Background(), `
		TRUNCATE TABLE inbox;
		DROP TABLE IF EXISTS orders;
		CREATE TABLE orders (id text PRIMARY KEY);
	`); err != nil {
		t.Fatalf("reset domain tables: %v", err)
	}
	return d
}

type order struct {
	ID string `json:"id"`
}

func msgWithID(id string, body order) natsclient.Msg[order] {
	return natsclient.Msg[order]{
		Data:    body,
		Subject: "orders.created",
		Headers: map[string][]string{inboxnats.NatsMsgIDHeader: {id}},
	}
}

func TestWrap_FirstDeliveryRunsFnInsideTx(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()
	calls := 0
	handler := inboxnats.Wrap[order]("svc:orders", d,
		func(ctx context.Context, tx *db.Tx, m natsclient.Msg[order]) error {
			calls++
			_, err := tx.Exec(ctx, "INSERT INTO orders VALUES ($1)", m.Data.ID)
			return err
		})

	err := handler(ctx, msgWithID("msg-1", order{ID: "o1"}))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}

	// Both rows must exist — proves the inbox row + domain insert
	// landed inside one Tx.
	var n int
	if err := d.QueryRow(ctx, "SELECT count(*) FROM orders WHERE id='o1'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("orders row = %d, want 1", n)
	}
	if err := d.QueryRow(ctx,
		"SELECT count(*) FROM inbox WHERE consumer='svc:orders' AND event_id='msg-1'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("inbox row = %d, want 1", n)
	}
}

func TestWrap_RedeliveryWithSameMsgIDIsDedup(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()
	calls := 0
	handler := inboxnats.Wrap[order]("svc:orders", d,
		func(ctx context.Context, tx *db.Tx, m natsclient.Msg[order]) error {
			calls++
			_, err := tx.Exec(ctx, "INSERT INTO orders VALUES ($1)", m.Data.ID)
			return err
		})

	msg := msgWithID("msg-dup", order{ID: "o1"})
	for i := 0; i < 5; i++ {
		if err := handler(ctx, msg); err != nil {
			t.Fatalf("delivery %d: %v", i, err)
		}
	}

	if calls != 1 {
		t.Errorf("fn calls = %d, want 1 (dedup contract violated)", calls)
	}
	var n int
	if err := d.QueryRow(ctx, "SELECT count(*) FROM orders").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("orders row = %d, want 1 (side effect ran more than once)", n)
	}
}

func TestWrap_FnErrorAllowsRetry(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()

	boom := errors.New("transient down")
	attempt := 0
	handler := inboxnats.Wrap[order]("svc:orders", d,
		func(ctx context.Context, tx *db.Tx, m natsclient.Msg[order]) error {
			attempt++
			if attempt == 1 {
				return boom // first delivery fails → Nak → retry
			}
			return nil
		})

	msg := msgWithID("msg-retry", order{ID: "x"})
	if err := handler(ctx, msg); err == nil {
		t.Fatal("expected first delivery to fail")
	}
	if err := handler(ctx, msg); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if attempt != 2 {
		t.Errorf("attempts = %d, want 2", attempt)
	}

	var n int
	if err := d.QueryRow(ctx,
		"SELECT count(*) FROM inbox WHERE event_id='msg-retry'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("inbox row after success = %d, want 1", n)
	}
}

func TestWrap_DifferentConsumersIndependent(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()

	calls := 0
	mkHandler := func(consumer string) func(ctx context.Context, m natsclient.Msg[order]) error {
		return inboxnats.Wrap[order](consumer, d,
			func(ctx context.Context, tx *db.Tx, m natsclient.Msg[order]) error {
				calls++
				return nil
			})
	}

	msg := msgWithID("msg-x", order{ID: "x"})
	if err := mkHandler("svc:a")(ctx, msg); err != nil {
		t.Fatalf("consumer A: %v", err)
	}
	if err := mkHandler("svc:b")(ctx, msg); err != nil {
		t.Fatalf("consumer B: %v", err)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2 (one per consumer namespace)", calls)
	}
}

func TestWrap_CustomEventIDFnUsesSequence(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()

	// Publisher does not stamp Nats-Msg-Id. Use Sequence as the fallback id.
	calls := 0
	handler := inboxnats.Wrap[order]("svc:seq", d,
		func(ctx context.Context, tx *db.Tx, m natsclient.Msg[order]) error {
			calls++
			return nil
		},
		inboxnats.WithEventIDFn(func(_ map[string][]string, subject string, seq uint64) string {
			return subject + ":" + strconv.FormatUint(seq, 10)
		}),
	)

	m := natsclient.Msg[order]{
		Data:     order{ID: "x"},
		Subject:  "orders.created",
		Sequence: 42,
		Headers:  map[string][]string{}, // intentionally empty
	}
	if err := handler(ctx, m); err != nil {
		t.Fatalf("first delivery: %v", err)
	}
	if err := handler(ctx, m); err != nil {
		t.Fatalf("redelivery: %v", err)
	}
	if calls != 1 {
		t.Errorf("fn calls = %d, want 1", calls)
	}
}
