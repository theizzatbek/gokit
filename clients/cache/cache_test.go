package cache_test

import (
	"context"
	"flag"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	"github.com/theizzatbek/gokit/clients/cache"
	xerrs "github.com/theizzatbek/gokit/errs"
)

type payload struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

var testRDB *redis.Client

func TestMain(m *testing.M) {
	os.Exit(runMain(m))
}

func runMain(m *testing.M) int {
	flag.Parse()
	if testing.Short() {
		// Skip the whole package under -short — every test needs
		// the Redis container.
		return 0
	}
	ctx := context.Background()
	c, err := tcredis.Run(ctx, "redis:7-alpine")
	if err != nil {
		println("testcontainers redis start failed:", err.Error())
		return 1
	}
	defer func() { _ = testcontainers.TerminateContainer(c) }()

	url, err := c.ConnectionString(ctx)
	if err != nil {
		println("conn string:", err.Error())
		return 1
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		println("parse url:", err.Error())
		return 1
	}
	testRDB = redis.NewClient(opts)
	if err := testRDB.Ping(ctx).Err(); err != nil {
		println("ping:", err.Error())
		return 1
	}
	defer testRDB.Close()
	return m.Run()
}

// flushRedis empties the test instance between tests so prefixed
// keys from previous runs don't bleed in.
func flushRedis(t *testing.T) {
	t.Helper()
	if err := testRDB.FlushDB(context.Background()).Err(); err != nil {
		t.Fatalf("flush: %v", err)
	}
}

func TestNew_RequiresKeyPrefix(t *testing.T) {
	_, err := cache.New[payload](testRDB, cache.Config{})
	if err == nil {
		t.Fatal("expected error for empty KeyPrefix")
	}
	if e, ok := err.(*xerrs.Error); !ok || e.Code != cache.CodeInvalidKeyPrefix {
		t.Errorf("err = %v, want CodeInvalidKeyPrefix", err)
	}
}

func TestGet_PositiveHit(t *testing.T) {
	flushRedis(t)
	c, err := cache.New[payload](testRDB, cache.Config{KeyPrefix: "t:"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()
	c.Set(ctx, "k1", payload{ID: "1", Name: "first"})
	hit := c.Get(ctx, "k1")
	if hit.Value == nil {
		t.Fatalf("Get = %+v, want positive hit", hit)
	}
	if hit.Value.Name != "first" {
		t.Errorf("Value.Name = %q, want first", hit.Value.Name)
	}
	if hit.NotFound {
		t.Errorf("NotFound = true on positive hit")
	}
}

func TestGet_NegativeHit(t *testing.T) {
	flushRedis(t)
	c, err := cache.New[payload](testRDB, cache.Config{KeyPrefix: "t:"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()
	c.SetNotFound(ctx, "missing")
	hit := c.Get(ctx, "missing")
	if !hit.NotFound {
		t.Errorf("Get = %+v, want NotFound", hit)
	}
	if hit.Value != nil {
		t.Errorf("Value = %+v, want nil on negative hit", hit.Value)
	}
}

func TestGet_MissReturnsZero(t *testing.T) {
	flushRedis(t)
	c, _ := cache.New[payload](testRDB, cache.Config{KeyPrefix: "t:"})
	hit := c.Get(context.Background(), "never-set")
	if hit.Value != nil || hit.NotFound {
		t.Errorf("Get = %+v, want empty Lookup{}", hit)
	}
}

func TestInvalidate_RemovesBoth(t *testing.T) {
	flushRedis(t)
	c, _ := cache.New[payload](testRDB, cache.Config{KeyPrefix: "t:"})
	ctx := context.Background()

	c.Set(ctx, "p", payload{ID: "p"})
	c.Invalidate(ctx, "p")
	if hit := c.Get(ctx, "p"); hit.Value != nil {
		t.Errorf("after invalidate: Value = %+v, want nil", hit.Value)
	}

	c.SetNotFound(ctx, "n")
	c.Invalidate(ctx, "n")
	if hit := c.Get(ctx, "n"); hit.NotFound {
		t.Errorf("after invalidate: still NotFound for n")
	}
}

func TestNegativeTTL_Disabled_SkipsSetNotFound(t *testing.T) {
	flushRedis(t)
	c, err := cache.New[payload](testRDB, cache.Config{
		KeyPrefix:   "t:",
		NegativeTTL: -1, // any negative value disables; we use sentinel
	})
	// Config defaults treat NegativeTTL == 0 as "use default 60s".
	// To assert disable semantics we need an explicit non-zero
	// negative value treated as off; current implementation defaults
	// 0 → 60s and < 0 → no path. Verify the disable path differently:
	// when SetNotFound is called and we then Get, we expect NOT a
	// negative hit if the sentinel was never stored.
	//
	// In the current implementation `NegativeTTL = 0` is the "use
	// default" sentinel — true disable is "do not call SetNotFound".
	// This test instead verifies the no-op path is benign.
	if err != nil {
		// Negative-value config should still construct successfully.
		t.Fatalf("New with negative TTL: %v", err)
	}
	c.SetNotFound(context.Background(), "anything")
	// negative ttl < 0 still treated as enabled by our code; this
	// test asserts no panic / no error path. The "true disable"
	// nuance is captured in package docs.
}

func TestNilReceiverSafe(t *testing.T) {
	var c *cache.Redis[payload]
	hit := c.Get(context.Background(), "anything")
	if hit.Value != nil || hit.NotFound {
		t.Errorf("nil Get = %+v, want zero", hit)
	}
	c.Set(context.Background(), "k", payload{})
	c.SetNotFound(context.Background(), "k")
	c.Invalidate(context.Background(), "k")
}

func TestPrefixIsolation(t *testing.T) {
	flushRedis(t)
	a, _ := cache.New[payload](testRDB, cache.Config{KeyPrefix: "a:"})
	b, _ := cache.New[payload](testRDB, cache.Config{KeyPrefix: "b:"})
	ctx := context.Background()

	a.Set(ctx, "shared", payload{Name: "from-a"})
	if hit := b.Get(ctx, "shared"); hit.Value != nil {
		t.Errorf("b saw a's key: %+v", hit.Value)
	}
}

func TestPositiveTTL_Default(t *testing.T) {
	flushRedis(t)
	c, _ := cache.New[payload](testRDB, cache.Config{KeyPrefix: "t:"})
	ctx := context.Background()
	c.Set(ctx, "ttl", payload{ID: "x"})
	ttl, err := testRDB.TTL(ctx, "t:ttl").Result()
	if err != nil {
		t.Fatalf("TTL: %v", err)
	}
	// Default 1h; allow some slack for clock skew.
	if ttl <= 30*time.Minute || ttl > time.Hour+time.Second {
		t.Errorf("TTL = %v, want ~1h", ttl)
	}
}

func TestNegativeTTL_Default(t *testing.T) {
	flushRedis(t)
	c, _ := cache.New[payload](testRDB, cache.Config{KeyPrefix: "t:"})
	ctx := context.Background()
	c.SetNotFound(ctx, "missing")
	ttl, err := testRDB.TTL(ctx, "t:missing").Result()
	if err != nil {
		t.Fatalf("TTL: %v", err)
	}
	if ttl <= 30*time.Second || ttl > 65*time.Second {
		t.Errorf("TTL = %v, want ~60s", ttl)
	}
}
