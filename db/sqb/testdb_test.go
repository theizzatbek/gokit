package sqb_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	tcpg "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/theizzatbek/fibermap/db"
)

var (
	pgOnce sync.Once
	pgCfg  db.Config
	pgErr  error
)

func startTestSqbDB(t *testing.T) *db.DB {
	t.Helper()
	pgOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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
			ConnectTimeout: 5 * time.Second,
			// Match parent harness invariant: pool of 1 keeps SET search_path sticky.
			MaxConns: 1, MinConns: 1,
		}
	})
	if pgErr != nil {
		t.Fatalf("postgres: %v", pgErr)
	}

	d, err := db.Connect(context.Background(), pgCfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(d.Close)

	schema := "test_" + randomHex(6)
	if _, err := d.Exec(context.Background(), fmt.Sprintf("CREATE SCHEMA %s", schema)); err != nil {
		t.Fatalf("schema: %v", err)
	}
	if _, err := d.Exec(context.Background(), fmt.Sprintf("SET search_path TO %s", schema)); err != nil {
		t.Fatalf("search_path: %v", err)
	}
	return d
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
