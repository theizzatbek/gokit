package refreshredis

import (
	"context"
	"flag"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	"github.com/theizzatbek/fibermap/auth"
)

var testRedis *redis.Client

func TestMain(m *testing.M) {
	os.Exit(runMain(m))
}

func runMain(m *testing.M) int {
	flag.Parse()
	if testing.Short() {
		return m.Run()
	}
	ctx := context.Background()
	c, err := tcredis.Run(ctx, "redis:7-alpine")
	if err != nil {
		println("testcontainers redis start failed:", err.Error())
		return 1
	}
	defer func() {
		if termErr := testcontainers.TerminateContainer(c); termErr != nil {
			println("testcontainers terminate:", termErr.Error())
		}
	}()
	endpoint, err := c.Endpoint(ctx, "")
	if err != nil {
		println("redis endpoint:", err.Error())
		return 1
	}
	testRedis = redis.NewClient(&redis.Options{Addr: endpoint})
	defer testRedis.Close()
	if err := testRedis.Ping(ctx).Err(); err != nil {
		println("redis ping:", err.Error())
		return 1
	}
	return m.Run()
}

func newStore(t *testing.T) *Store {
	t.Helper()
	if testRedis == nil {
		t.Skip("integration test — Docker required")
	}
	if err := testRedis.FlushAll(context.Background()).Err(); err != nil {
		t.Fatalf("flushall: %v", err)
	}
	return New(testRedis)
}

func TestIssueAndDirectRead(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	ctx := context.Background()
	s := newStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	var h [32]byte
	h[0] = 0xAB
	rec := auth.Record{
		TokenHash: h, Subject: "u-1",
		FamilyID:  "00000000-0000-0000-0000-000000000001",
		IssuedAt:  now,
		ExpiresAt: now.Add(time.Hour),
	}
	if err := s.Issue(ctx, rec); err != nil {
		t.Fatalf("issue: %v", err)
	}
	if got := testRedis.Exists(ctx, refreshKey(h)).Val(); got != 1 {
		t.Fatalf("refresh key missing")
	}
	if got := testRedis.SIsMember(ctx, familyKey(rec.FamilyID), hashToHex(h)).Val(); !got {
		t.Fatalf("family set missing token")
	}
}
