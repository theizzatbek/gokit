package testdb

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcpg "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/theizzatbek/gokit/db"
)

// sharedContainer is the process-wide single-node container reused by
// [Spin] calls that did NOT pass [WithFreshPerTest]. Lazy init via
// shared.once on the first call. Termination is intentionally NOT
// handled here — testcontainers' Reaper ("Ryuk") tears the container
// down when the test binary exits. A previous version used a
// per-caller refcount to terminate when refs hit 0, but that broke
// sequential tests: refs went 1→0→1 between tests, the cleanup
// terminated the container at refs==0, and the next Spin reused
// stale shared.cfg pointing at the dead container.
//
// The reuse pattern intentionally trades a bit of cross-test
// coupling (everything lands in the same physical DB) for a 5-10x
// speedup on suites that touch Postgres in every test.
type sharedContainer struct {
	once sync.Once
	cfg  db.Config
	err  error
}

var shared sharedContainer

// Spin spins (lazily, once per test binary by default) a Postgres
// container and returns a connected *db.DB bound to a freshly-named
// schema for per-test isolation.
//
// Default behaviour:
//   - One container per test binary, reused across calls (5-10x
//     faster than fresh-per-test). Pass [WithFreshPerTest] to opt
//     out.
//   - Per-call schema isolation: a `test_<hex>` schema is created
//     and the connection's `search_path` is pinned to it.
//   - Cleanup registered via t.Cleanup (Close + schema DROP).
//
// Skips the calling test under `go test -short`.
func Spin(t *testing.T, opts ...Option) *db.DB {
	t.Helper()
	if testing.Short() {
		t.Skip("requires Postgres testcontainer; rerun without -short")
	}
	cfg := applyOptions(opts)
	if cfg.freshPerTest {
		d, _ := spinFresh(t, cfg)
		return d
	}
	return spinShared(t, cfg)
}

// spinShared bootstraps (or reuses) the process-wide container and
// returns a *db.DB scoped to a fresh schema. Container teardown is
// delegated to testcontainers Reaper (Ryuk) at process exit.
func spinShared(t *testing.T, cfg config) *db.DB {
	shared.once.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), cfg.startupTimeout)
		defer cancel()
		_, dbCfg, err := startSinglePostgres(ctx, cfg)
		if err != nil {
			shared.err = err
			return
		}
		shared.cfg = dbCfg
	})
	if shared.err != nil {
		t.Fatalf("testdb: shared container init: %v", shared.err)
	}

	d, err := db.Connect(context.Background(), shared.cfg)
	if err != nil {
		t.Fatalf("testdb: connect to shared container: %v", err)
	}

	schema := "test_" + randomHex(6)
	if _, err := d.Pool().Exec(context.Background(),
		fmt.Sprintf("CREATE SCHEMA %s", schema)); err != nil {
		d.Close()
		t.Fatalf("testdb: create schema %s: %v", schema, err)
	}
	if _, err := d.Pool().Exec(context.Background(),
		fmt.Sprintf("SET search_path TO %s", schema)); err != nil {
		d.Close()
		t.Fatalf("testdb: set search_path: %v", err)
	}
	t.Cleanup(func() {
		_, _ = d.Pool().Exec(context.Background(),
			fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", schema))
		d.Close()
	})
	return d
}

// spinFresh builds a brand-new container, returns *db.DB + a teardown
// closure (registered with t.Cleanup). Always honoured for callers
// who pass [WithFreshPerTest].
func spinFresh(t *testing.T, cfg config) (*db.DB, func()) {
	ctx, cancel := context.WithTimeout(context.Background(), cfg.startupTimeout)
	defer cancel()
	c, dbCfg, err := startSinglePostgres(ctx, cfg)
	if err != nil {
		t.Fatalf("testdb: fresh container init: %v", err)
	}
	d, err := db.Connect(context.Background(), dbCfg)
	if err != nil {
		_ = testcontainers.TerminateContainer(c)
		t.Fatalf("testdb: connect to fresh container: %v", err)
	}
	teardown := func() {
		d.Close()
		_ = testcontainers.TerminateContainer(c)
	}
	t.Cleanup(teardown)
	return d, teardown
}

// startSinglePostgres is the shared `tcpg.Run + db.Config build`
// recipe used by both spinShared and spinFresh. Returns the running
// container plus a populated db.Config ready for db.Connect.
func startSinglePostgres(ctx context.Context, cfg config) (testcontainers.Container, db.Config, error) {
	c, err := tcpg.Run(ctx, cfg.image,
		tcpg.WithDatabase(cfg.database),
		tcpg.WithUsername(cfg.username),
		tcpg.WithPassword(cfg.password),
		tcpg.BasicWaitStrategies(),
	)
	if err != nil {
		return nil, db.Config{}, fmt.Errorf("run container: %w", err)
	}
	host, err := c.Host(ctx)
	if err != nil {
		_ = testcontainers.TerminateContainer(c)
		return nil, db.Config{}, err
	}
	port, err := c.MappedPort(ctx, "5432/tcp")
	if err != nil {
		_ = testcontainers.TerminateContainer(c)
		return nil, db.Config{}, err
	}
	p, _ := strconv.Atoi(port.Port())
	return c, db.Config{
		Host:           host,
		Port:           p,
		User:           cfg.username,
		Password:       cfg.password,
		Database:       cfg.database,
		SSLMode:        "disable",
		ConnectTimeout: 5 * time.Second,
		// MaxConns=1 / MinConns=1 keeps SET search_path effective —
		// the schema-pinning we do in spinShared depends on every
		// query landing on the same connection.
		MaxConns: 1,
		MinConns: 1,
	}, nil
}

// randomHex returns 2n hex chars from crypto/rand. Used to mint
// per-test schema names; not security-sensitive but math/rand would
// collide too often on parallel tests.
func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
