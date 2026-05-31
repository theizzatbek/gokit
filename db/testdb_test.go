package db_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	tcpg "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/theizzatbek/gokit/db"
)

var (
	pgOnce sync.Once
	pgCfg  db.Config
	pgErr  error
)

func TestMain(m *testing.M) {
	code := m.Run()
	os.Exit(code)
}

// startTestDB spins (lazily, once per test binary) a Postgres container and
// returns a *db.DB bound to a fresh, randomly-named schema for isolation.
//
// Skips the calling test under `go test -short` so unit tests in this
// package (config / tracer / errors / metrics — none of which call
// startTestDB) still run while integration tests are bypassed.
func startTestDB(t *testing.T, opts ...db.Option) *db.DB {
	t.Helper()
	if testing.Short() {
		t.Skip("requires Postgres testcontainer; rerun without -short")
	}
	pgOnce.Do(initPostgresContainer)
	if pgErr != nil {
		t.Fatalf("postgres container: %v", pgErr)
	}

	cfg := pgCfg
	d, err := db.Connect(context.Background(), cfg, opts...)
	if err != nil {
		t.Fatalf("db.Connect: %v", err)
	}
	t.Cleanup(d.Close)

	// Use d.Pool() directly for schema setup: *DB.Exec arrives in Task 4.
	schema := "test_" + randomHex(6)
	if _, err := d.Pool().Exec(context.Background(),
		fmt.Sprintf("CREATE SCHEMA %s", schema)); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	if _, err := d.Pool().Exec(context.Background(),
		fmt.Sprintf("SET search_path TO %s", schema)); err != nil {
		t.Fatalf("set search_path: %v", err)
	}
	return d
}

func initPostgresContainer() {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// tcpg.DefaultWaitStrategy() does not exist in v0.42.0.
	// BasicWaitStrategies() returns a CustomizeRequestOption (not a wait.Strategy),
	// so it is passed directly to Run — not wrapped in WithWaitStrategyAndDeadline.
	// It waits for "database system is ready to accept connections" twice (Postgres
	// restarts once on first boot) and then for the port to be reachable, which
	// avoids flakiness on macOS/Windows with Docker Desktop proxies.
	c, err := tcpg.Run(ctx, "postgres:16-alpine",
		tcpg.WithDatabase("test"),
		tcpg.WithUsername("test"),
		tcpg.WithPassword("test"),
		tcpg.BasicWaitStrategies(),
	)
	if err != nil {
		pgErr = fmt.Errorf("run container: %w", err)
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
		Host: host, Port: p, User: "test", Password: "test",
		Database: "test", SSLMode: "disable",
		ConnectTimeout: 5 * time.Second,
		// MaxConns=1 / MinConns=1: SET search_path runs against the only conn in
		// the pool, so it persists for every subsequent query in the test. Without
		// this, pgxpool can hand a different connection on the next query and
		// schema isolation silently breaks.
		MaxConns: 1,
		MinConns: 1,
	}
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
