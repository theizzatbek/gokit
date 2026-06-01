package auditpg_test

import (
	"context"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tcpg "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/theizzatbek/gokit/audit"
	"github.com/theizzatbek/gokit/audit/auditpg"
	"github.com/theizzatbek/gokit/db"
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
	host, _ := c.Host(ctx)
	port, _ := c.MappedPort(ctx, "5432/tcp")
	p, _ := strconv.Atoi(port.Port())
	pgCfg = db.Config{
		Host: host, Port: p, User: "test", Password: "test",
		Database: "test", SSLMode: "disable",
		MaxConns: 4, MinConns: 2,
	}
}

func freshDB(t *testing.T) *db.DB {
	t.Helper()
	if testing.Short() {
		t.Skip("requires Postgres container")
	}
	pgOnce.Do(initPostgresContainer)
	if pgErr != nil {
		t.Fatalf("postgres: %v", pgErr)
	}
	d, err := db.Connect(context.Background(), pgCfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := auditpg.ApplySchema(context.Background(), d); err != nil {
		t.Fatalf("schema: %v", err)
	}
	if _, err := d.Exec(context.Background(), "TRUNCATE audit_events"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func newLogger(t *testing.T, opts ...audit.Option) *audit.Logger {
	t.Helper()
	d := freshDB(t)
	l, err := audit.New(auditpg.New(d), audit.Config{ServiceName: "test"}, opts...)
	if err != nil {
		t.Fatalf("audit.New: %v", err)
	}
	return l
}

func TestAppend_RoundTrip(t *testing.T) {
	l := newLogger(t)
	id, err := l.Log(context.Background(), audit.Event{
		Action: "user.created", Outcome: audit.Success,
		Actor:    audit.Actor{Subject: "u-1", IP: "1.2.3.4"},
		Target:   audit.Target{Type: "user", ID: "u-99"},
		Metadata: map[string]any{"plan": "pro"},
	})
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	out, err := l.Query(context.Background(), audit.Filter{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("len = %d, want 1", len(out))
	}
	if out[0].ID != id {
		t.Errorf("ID = %q, want %q", out[0].ID, id)
	}
	if out[0].Actor.IP != "1.2.3.4" {
		t.Errorf("Actor.IP = %q", out[0].Actor.IP)
	}
	if out[0].Metadata["plan"] != "pro" {
		t.Errorf("Metadata = %v", out[0].Metadata)
	}
}

func TestQuery_FilterByActor(t *testing.T) {
	l := newLogger(t)
	for _, sub := range []string{"u-1", "u-2", "u-1"} {
		_ = l.Login(context.Background(), audit.Actor{Subject: sub}, audit.Success)
	}
	out, _ := l.Query(context.Background(), audit.Filter{Actor: "u-1"})
	if len(out) != 2 {
		t.Errorf("len = %d, want 2", len(out))
	}
}

func TestQuery_FilterByActionWildcard(t *testing.T) {
	l := newLogger(t)
	_, _ = l.Log(context.Background(), audit.Event{Action: "user.created", Outcome: audit.Success})
	_, _ = l.Log(context.Background(), audit.Event{Action: "user.deleted", Outcome: audit.Success})
	_, _ = l.Log(context.Background(), audit.Event{Action: "post.created", Outcome: audit.Success})
	out, _ := l.Query(context.Background(), audit.Filter{Action: "user.*"})
	if len(out) != 2 {
		t.Errorf("len = %d, want 2", len(out))
	}
}

func TestPurgeBefore_DeletesOld(t *testing.T) {
	l := newLogger(t)
	for i := 0; i < 3; i++ {
		_, _ = l.Log(context.Background(), audit.Event{
			OccurredAt: time.Now().Add(-2 * time.Hour).Add(time.Duration(i) * time.Second),
			Action:     "x.y", Outcome: audit.Success,
		})
	}
	_, _ = l.Log(context.Background(), audit.Event{Action: "fresh", Outcome: audit.Success})

	n, err := l.PurgeBefore(context.Background(), time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if n != 3 {
		t.Errorf("purged = %d, want 3", n)
	}
	out, _ := l.Query(context.Background(), audit.Filter{})
	if len(out) != 1 || out[0].Action != "fresh" {
		t.Errorf("residual = %+v", out)
	}
}

func TestHashChain_BuildsAndVerifiesViaPostgres(t *testing.T) {
	l := newLogger(t, audit.WithHashChain())
	for i := 0; i < 5; i++ {
		_, err := l.Log(context.Background(), audit.Event{
			OccurredAt: time.Now().Add(time.Duration(i) * time.Millisecond),
			Action:     "x.y", Outcome: audit.Success,
			Actor: audit.Actor{Subject: "u-1"},
		})
		if err != nil {
			t.Fatalf("Log[%d]: %v", i, err)
		}
	}
	out, _ := l.Query(context.Background(), audit.Filter{})
	if err := audit.Verify(out); err != nil {
		t.Errorf("Verify: %v", err)
	}
	if len(out) != 5 {
		t.Errorf("len = %d, want 5", len(out))
	}
}

func TestHashChain_ConcurrentWritersSerialized(t *testing.T) {
	l := newLogger(t, audit.WithHashChain())
	const writers = 5
	const perWriter = 10
	var wg sync.WaitGroup
	var failures int64
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				if _, err := l.Log(context.Background(), audit.Event{
					Action: "x.y", Outcome: audit.Success,
				}); err != nil {
					atomic.AddInt64(&failures, 1)
				}
			}
		}()
	}
	wg.Wait()
	if failures > 0 {
		t.Fatalf("concurrent writers failures: %d", failures)
	}
	out, _ := l.Query(context.Background(), audit.Filter{})
	if len(out) != writers*perWriter {
		t.Errorf("event count = %d, want %d", len(out), writers*perWriter)
	}
	if err := audit.Verify(out); err != nil {
		t.Errorf("chain broke under concurrency: %v", err)
	}
}

func TestLastHash_EmptyTableReturnsNil(t *testing.T) {
	d := freshDB(t)
	s := auditpg.New(d)
	h, err := s.LastHash(context.Background())
	if err != nil {
		t.Fatalf("LastHash: %v", err)
	}
	if h != nil {
		t.Errorf("LastHash on empty = %x, want nil", h)
	}
}

func TestQuery_LimitOffsetOrderASC(t *testing.T) {
	l := newLogger(t)
	for i := 0; i < 10; i++ {
		_, _ = l.Log(context.Background(), audit.Event{
			OccurredAt: time.Now().Add(time.Duration(i) * time.Millisecond),
			Action:     "x.y", Outcome: audit.Success,
		})
	}
	out, _ := l.Query(context.Background(), audit.Filter{Limit: 3, Offset: 2})
	if len(out) != 3 {
		t.Errorf("len = %d, want 3", len(out))
	}
	// ASC ordering: each subsequent OccurredAt >= prior.
	for i := 1; i < len(out); i++ {
		if out[i].OccurredAt.Before(out[i-1].OccurredAt) {
			t.Errorf("not ASC at index %d", i)
		}
	}
}
