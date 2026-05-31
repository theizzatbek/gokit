package redisclient

import (
	"context"
	"flag"
	"os"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/testcontainers/testcontainers-go"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	xerrs "github.com/theizzatbek/gokit/errs"
)

var testURL string

func TestMain(m *testing.M) {
	os.Exit(runMain(m))
}

func runMain(m *testing.M) int {
	flag.Parse()
	if testing.Short() {
		// Skip the whole package under -short — every test in this
		// file needs the Redis container.
		return 0
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

	url, err := c.ConnectionString(ctx)
	if err != nil {
		println("connection string:", err.Error())
		return 1
	}
	testURL = url
	return m.Run()
}

func TestConnect_Success(t *testing.T) {
	c, err := Connect(context.Background(), Config{URL: testURL})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	if c.Redis() == nil {
		t.Error("Redis() returned nil after successful Connect")
	}
}

func TestConnect_MissingURL(t *testing.T) {
	_, err := Connect(context.Background(), Config{})
	if err == nil {
		t.Fatal("expected error for empty URL")
	}
	if e, ok := err.(*xerrs.Error); !ok || e.Code != CodeMissingURL {
		t.Errorf("err = %v, want CodeMissingURL", err)
	}
}

func TestConnect_InvalidURL(t *testing.T) {
	_, err := Connect(context.Background(), Config{URL: "not-a-url"})
	if err == nil {
		t.Fatal("expected error for malformed URL")
	}
	if e, ok := err.(*xerrs.Error); !ok || e.Code != CodeInvalidURL {
		t.Errorf("err = %v, want CodeInvalidURL", err)
	}
}

func TestConnect_RetriesOnUnreachable(t *testing.T) {
	// 127.0.0.1:1 is reserved and almost certainly refuses.
	start := time.Now()
	_, err := Connect(context.Background(), Config{
		URL:                "redis://127.0.0.1:1",
		ConnectMaxRetries:  2,
		ConnectBackoffBase: 10 * time.Millisecond,
		ConnectBackoffMax:  20 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("expected connect failure against 127.0.0.1:1")
	}
	if e, ok := err.(*xerrs.Error); !ok || e.Code != CodeConnectFailed {
		t.Errorf("err = %v, want CodeConnectFailed", err)
	}
	// 2 retries × at least the backoff base; allow some slack.
	if elapsed := time.Since(start); elapsed < 10*time.Millisecond {
		t.Errorf("retry loop completed in %v — too fast, didn't actually retry", elapsed)
	}
}

func TestConnect_CancelDuringBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	_, err := Connect(ctx, Config{
		URL:                "redis://127.0.0.1:1",
		ConnectMaxRetries:  10,
		ConnectBackoffBase: 200 * time.Millisecond,
		ConnectBackoffMax:  1 * time.Second,
	})
	if err == nil {
		t.Fatal("expected connect failure on cancelled ctx")
	}
	if e, ok := err.(*xerrs.Error); !ok || e.Code != CodeConnectFailed {
		t.Errorf("err = %v, want CodeConnectFailed", err)
	}
}

func TestClient_Close_Idempotent(t *testing.T) {
	c, err := Connect(context.Background(), Config{URL: testURL})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Errorf("first Close: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

func TestClient_NilReceiver_CloseSafe(t *testing.T) {
	var c *Client
	if err := c.Close(); err != nil {
		t.Errorf("nil-receiver Close: %v", err)
	}
	if r := c.Redis(); r != nil {
		t.Errorf("nil-receiver Redis() = %v, want nil", r)
	}
}

func TestRedisRoundtrip(t *testing.T) {
	c, err := Connect(context.Background(), Config{URL: testURL})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	ctx := context.Background()
	if err := c.Redis().Set(ctx, "k1", "v1", 0).Err(); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := c.Redis().Get(ctx, "k1").Result()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "v1" {
		t.Errorf("Get = %q, want v1", got)
	}
}

func TestMetrics_HookFiresOnCommand(t *testing.T) {
	reg := prometheus.NewRegistry()
	c, err := Connect(context.Background(), Config{URL: testURL}, WithMetrics(reg))
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	if err := c.Redis().Set(context.Background(), "metric_key", "x", 0).Err(); err != nil {
		t.Fatal(err)
	}

	if got := commandCount(t, reg, "set", "success"); got < 1 {
		t.Errorf("redis_commands_total{cmd=set,outcome=success} = %v, want >= 1", got)
	}
}

func TestMetrics_RedisNilCountsAsSuccess(t *testing.T) {
	reg := prometheus.NewRegistry()
	c, err := Connect(context.Background(), Config{URL: testURL}, WithMetrics(reg))
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	// Missing key returns redis.Nil — operationally a hit-miss, not
	// a failure. observe maps it to outcome=success.
	_, _ = c.Redis().Get(context.Background(), "definitely-not-set").Result()

	if got := commandCount(t, reg, "get", "error"); got != 0 {
		t.Errorf("redis_commands_total{cmd=get,outcome=error} = %v, want 0 (redis.Nil is not an error)", got)
	}
	if got := commandCount(t, reg, "get", "success"); got < 1 {
		t.Errorf("redis_commands_total{cmd=get,outcome=success} = %v, want >= 1", got)
	}
}

// commandCount reads redis_commands_total{cmd,outcome} from reg.
func commandCount(t *testing.T, reg *prometheus.Registry, cmd, outcome string) float64 {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() != "redis_commands_total" {
			continue
		}
		for _, m := range mf.Metric {
			labels := map[string]string{}
			for _, l := range m.GetLabel() {
				labels[l.GetName()] = l.GetValue()
			}
			if labels["cmd"] == cmd && labels["outcome"] == outcome {
				return m.GetCounter().GetValue()
			}
		}
	}
	return 0
}

// silence unused-import warning when testutil isn't exercised yet.
var _ = testutil.CollectAndCount
