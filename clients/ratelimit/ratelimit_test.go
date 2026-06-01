package ratelimit_test

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	"github.com/theizzatbek/gokit/clients/ratelimit"
	redisclient "github.com/theizzatbek/gokit/clients/redis"
	xerrs "github.com/theizzatbek/gokit/errs"
)

var testRC *redisclient.Client

func TestMain(m *testing.M) { os.Exit(runMain(m)) }

func runMain(m *testing.M) int {
	flag.Parse()
	if testing.Short() {
		return 0
	}
	ctx := context.Background()
	c, err := tcredis.Run(ctx, "redis:7-alpine")
	if err != nil {
		fmt.Println("testcontainers redis start failed:", err.Error())
		return 1
	}
	defer func() { _ = testcontainers.TerminateContainer(c) }()

	url, err := c.ConnectionString(ctx)
	if err != nil {
		fmt.Println("conn string:", err.Error())
		return 1
	}
	rc, err := redisclient.Connect(ctx, redisclient.Config{URL: url})
	if err != nil {
		fmt.Println("connect:", err.Error())
		return 1
	}
	defer rc.Close()
	testRC = rc
	return m.Run()
}

func flushRedis(t *testing.T) {
	t.Helper()
	if err := testRC.Redis().FlushDB(context.Background()).Err(); err != nil {
		t.Fatalf("flush: %v", err)
	}
}

func TestNewRedis_RequiresKeyPrefix(t *testing.T) {
	_, err := ratelimit.NewRedis(testRC, ratelimit.Config{Limit: 5, Window: time.Second})
	if err == nil {
		t.Fatal("expected error for empty KeyPrefix")
	}
	if e, ok := err.(*xerrs.Error); !ok || e.Code != ratelimit.CodeInvalidConfig {
		t.Errorf("err = %v, want CodeInvalidConfig", err)
	}
}

func TestNewRedis_RequiresPositiveLimit(t *testing.T) {
	_, err := ratelimit.NewRedis(testRC, ratelimit.Config{KeyPrefix: "t:", Window: time.Second})
	if err == nil {
		t.Fatal("expected error for zero Limit")
	}
}

func TestNewRedis_RequiresPositiveWindow(t *testing.T) {
	_, err := ratelimit.NewRedis(testRC, ratelimit.Config{KeyPrefix: "t:", Limit: 5})
	if err == nil {
		t.Fatal("expected error for zero Window")
	}
}

func TestAllow_UnderLimit(t *testing.T) {
	flushRedis(t)
	l, err := ratelimit.NewRedis(testRC, ratelimit.Config{
		KeyPrefix: "rl1:", Limit: 3, Window: time.Second,
	})
	if err != nil {
		t.Fatalf("NewRedis: %v", err)
	}
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		allow, err := l.Allow(ctx, "u1")
		if err != nil {
			t.Fatalf("Allow #%d: %v", i, err)
		}
		if !allow.Allowed {
			t.Errorf("Allow #%d denied, want allowed", i)
		}
	}
}

func TestAllow_OverLimitDenied(t *testing.T) {
	flushRedis(t)
	l, _ := ratelimit.NewRedis(testRC, ratelimit.Config{
		KeyPrefix: "rl2:", Limit: 2, Window: 5 * time.Second,
	})
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		allow, _ := l.Allow(ctx, "u1")
		if !allow.Allowed {
			t.Fatalf("pre-flood #%d denied", i)
		}
	}
	allow, err := l.Allow(ctx, "u1")
	if err != nil {
		t.Fatalf("Allow over-limit returned err: %v", err)
	}
	if allow.Allowed {
		t.Error("Allowed=true past the cap, want denied")
	}
	if allow.RetryAfter <= 0 {
		t.Errorf("RetryAfter = %v, want > 0", allow.RetryAfter)
	}
	if allow.RetryAfter > 5*time.Second {
		t.Errorf("RetryAfter = %v exceeds window", allow.RetryAfter)
	}
}

func TestAllow_RemainingDecreases(t *testing.T) {
	flushRedis(t)
	l, _ := ratelimit.NewRedis(testRC, ratelimit.Config{
		KeyPrefix: "rl3:", Limit: 5, Window: time.Second,
	})
	ctx := context.Background()
	allow1, _ := l.Allow(ctx, "u1")
	allow2, _ := l.Allow(ctx, "u1")
	if allow1.Remaining <= allow2.Remaining {
		t.Errorf("Remaining did not decrease across calls: %d → %d",
			allow1.Remaining, allow2.Remaining)
	}
	if allow1.Remaining != 4 {
		t.Errorf("Remaining after 1st = %d, want 4", allow1.Remaining)
	}
}

func TestAllow_WindowRollover(t *testing.T) {
	flushRedis(t)
	l, _ := ratelimit.NewRedis(testRC, ratelimit.Config{
		KeyPrefix: "rl4:", Limit: 1, Window: 300 * time.Millisecond,
	})
	ctx := context.Background()
	if a, _ := l.Allow(ctx, "u1"); !a.Allowed {
		t.Fatal("first call denied")
	}
	if a, _ := l.Allow(ctx, "u1"); a.Allowed {
		t.Fatal("second call allowed within window")
	}
	time.Sleep(350 * time.Millisecond)
	if a, _ := l.Allow(ctx, "u1"); !a.Allowed {
		t.Fatal("call after window rollover denied")
	}
}

func TestAllow_DifferentKeysIndependent(t *testing.T) {
	flushRedis(t)
	l, _ := ratelimit.NewRedis(testRC, ratelimit.Config{
		KeyPrefix: "rl5:", Limit: 1, Window: time.Second,
	})
	ctx := context.Background()
	if a, _ := l.Allow(ctx, "u1"); !a.Allowed {
		t.Fatal("u1 first denied")
	}
	if a, _ := l.Allow(ctx, "u2"); !a.Allowed {
		t.Fatal("u2 should be independent of u1's bucket")
	}
}

func TestAllow_ConcurrentExactlyLimitGranted(t *testing.T) {
	flushRedis(t)
	const limit = 10
	const requests = 100
	l, _ := ratelimit.NewRedis(testRC, ratelimit.Config{
		KeyPrefix: "rl6:", Limit: limit, Window: 5 * time.Second,
	})
	ctx := context.Background()
	var allowed int64
	var wg sync.WaitGroup
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if a, _ := l.Allow(ctx, "u1"); a.Allowed {
				atomic.AddInt64(&allowed, 1)
			}
		}()
	}
	wg.Wait()
	if got := atomic.LoadInt64(&allowed); got != limit {
		t.Errorf("allowed = %d, want exactly %d under concurrency", got, limit)
	}
}

func TestAllow_NilReceiverAllows(t *testing.T) {
	var l *ratelimit.Redis
	a, err := l.Allow(context.Background(), "x")
	if err != nil {
		t.Errorf("nil-receiver returned err: %v", err)
	}
	if !a.Allowed {
		t.Error("nil receiver denied — should fail-open")
	}
}

func TestNewRedis_NilClientAllowsAll(t *testing.T) {
	l, err := ratelimit.NewRedis(nil, ratelimit.Config{
		KeyPrefix: "rl:", Limit: 1, Window: time.Second,
	})
	if err != nil {
		t.Fatalf("NewRedis(nil): %v", err)
	}
	for i := 0; i < 5; i++ {
		a, err := l.Allow(context.Background(), "u")
		if err != nil {
			t.Fatalf("Allow with nil rdb: %v", err)
		}
		if !a.Allowed {
			t.Error("nil-rdb limiter denied — should pass through")
		}
	}
}

// confirm Limit/Window getters
func TestGetters(t *testing.T) {
	l, _ := ratelimit.NewRedis(testRC, ratelimit.Config{
		KeyPrefix: "rl:", Limit: 7, Window: 2 * time.Second,
	})
	if l.Limit() != 7 {
		t.Errorf("Limit() = %d, want 7", l.Limit())
	}
	if l.Window() != 2*time.Second {
		t.Errorf("Window() = %v, want 2s", l.Window())
	}
}

// compile-time interface assertion
var _ ratelimit.Limiter = (*ratelimit.Redis)(nil)

// silence import-unused if redis is dropped from a future refactor
var _ = redis.Nil
