package fibermount_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/theizzatbek/gokit/auth/fibermount"
	"github.com/theizzatbek/gokit/clients/ratelimit"
	"github.com/theizzatbek/gokit/fibermap"
)

// fakeLimiter is a deterministic in-memory Limiter for testing the
// fibermount factory without spinning Redis.
type fakeLimiter struct {
	limit   int
	window  time.Duration
	allowed int64
	denied  int64
	calls   int64
	allowFn func() bool
}

func (f *fakeLimiter) Allow(_ context.Context, _ string) (ratelimit.Allowance, error) {
	atomic.AddInt64(&f.calls, 1)
	allow := f.allowFn()
	if allow {
		atomic.AddInt64(&f.allowed, 1)
		return ratelimit.Allowance{
			Allowed: true, Remaining: f.limit - int(f.allowed),
			Limit: f.limit, Window: f.window,
		}, nil
	}
	atomic.AddInt64(&f.denied, 1)
	return ratelimit.Allowance{
		Allowed: false, Remaining: 0,
		Limit: f.limit, Window: f.window,
		RetryAfter: 2 * time.Second,
	}, nil
}

func (f *fakeLimiter) Limit() int            { return f.limit }
func (f *fakeLimiter) Window() time.Duration { return f.window }

func TestRateLimitRedisFactory_DeniesOverLimit(t *testing.T) {
	// Fake allows the first two calls then denies.
	var seen int64
	fl := &fakeLimiter{
		limit: 2, window: time.Minute,
		allowFn: func() bool { return atomic.AddInt64(&seen, 1) <= 2 },
	}

	app := fiber.New(fiber.Config{ErrorHandler: fibermap.ErrorHandler(nil)})
	eng := fibermap.New[appCtx]()
	eng.SetContextBuilder(func(c *fiber.Ctx) (appCtx, error) { return appCtx{}, nil })
	fibermap.RegisterHandler(eng, "ping", func(c *fibermap.Context[appCtx]) error {
		return c.Ctx.SendStatus(http.StatusOK)
	})
	if err := fibermount.MountRateLimitRedisFactory(eng, fl); err != nil {
		t.Fatalf("Mount: %v", err)
	}

	const yamlCfg = `
groups:
  - prefix: /api
    middleware:
      - { rate_limit_redis: [] }
    routes:
      - { method: GET, path: /ping, handler: ping }
`
	if err := eng.LoadBytes([]byte(yamlCfg)); err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if err := eng.Mount(app); err != nil {
		t.Fatalf("Mount engine: %v", err)
	}

	for i, want := range []int{200, 200, 429} {
		resp, err := app.Test(httptest.NewRequest("GET", "/api/ping", nil))
		if err != nil {
			t.Fatalf("Test #%d: %v", i, err)
		}
		if resp.StatusCode != want {
			t.Errorf("call #%d status = %d, want %d", i, resp.StatusCode, want)
		}
		if i == 2 {
			if got := resp.Header.Get("Retry-After"); got == "" {
				t.Errorf("denied response missing Retry-After header")
			}
		}
	}
}

func TestRateLimitRedisFactory_PropagatesRateLimitHeaders(t *testing.T) {
	fl := &fakeLimiter{limit: 10, window: time.Minute, allowFn: func() bool { return true }}

	app := fiber.New(fiber.Config{ErrorHandler: fibermap.ErrorHandler(nil)})
	eng := fibermap.New[appCtx]()
	eng.SetContextBuilder(func(c *fiber.Ctx) (appCtx, error) { return appCtx{}, nil })
	fibermap.RegisterHandler(eng, "ping", func(c *fibermap.Context[appCtx]) error {
		return c.Ctx.SendStatus(http.StatusOK)
	})
	_ = fibermount.MountRateLimitRedisFactory(eng, fl)
	if err := eng.LoadBytes([]byte(`groups:
  - prefix: /
    routes:
      - method: GET
        path: /p
        handler: ping
        middleware:
          - { rate_limit_redis: [] }
`)); err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if err := eng.Mount(app); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	resp, _ := app.Test(httptest.NewRequest("GET", "/p", nil))
	if got := resp.Header.Get("X-RateLimit-Limit"); got != "10" {
		t.Errorf("X-RateLimit-Limit = %q, want 10", got)
	}
	if got := resp.Header.Get("X-RateLimit-Remaining"); got == "" {
		t.Error("X-RateLimit-Remaining missing")
	}
}

func TestRateLimitRedisFactory_UnknownStrategyErrorsAtMount(t *testing.T) {
	fl := &fakeLimiter{limit: 1, window: time.Second, allowFn: func() bool { return true }}
	eng := fibermap.New[appCtx]()
	eng.SetContextBuilder(func(c *fiber.Ctx) (appCtx, error) { return appCtx{}, nil })
	fibermap.RegisterHandler(eng, "ping", func(c *fibermap.Context[appCtx]) error { return nil })
	_ = fibermount.MountRateLimitRedisFactory(eng, fl)
	if err := eng.LoadBytes([]byte(`groups:
  - prefix: /
    routes:
      - method: GET
        path: /p
        handler: ping
        middleware:
          - { rate_limit_redis: [bogus] }
`)); err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	app := fiber.New()
	err := eng.Mount(app)
	if err == nil {
		t.Fatal("expected mount to fail on unknown strategy")
	}
	if !strings.Contains(err.Error(), "unknown key strategy") {
		t.Errorf("err = %v, want mention of unknown key strategy", err)
	}
}

func TestRateLimitRedisFactory_UserStrategyWithoutKeyFnErrors(t *testing.T) {
	fl := &fakeLimiter{limit: 1, window: time.Second, allowFn: func() bool { return true }}
	eng := fibermap.New[appCtx]()
	eng.SetContextBuilder(func(c *fiber.Ctx) (appCtx, error) { return appCtx{}, nil })
	fibermap.RegisterHandler(eng, "ping", func(c *fibermap.Context[appCtx]) error { return nil })
	_ = fibermount.MountRateLimitRedisFactory(eng, fl)
	if err := eng.LoadBytes([]byte(`groups:
  - prefix: /
    routes:
      - method: GET
        path: /p
        handler: ping
        middleware:
          - { rate_limit_redis: [user] }
`)); err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	app := fiber.New()
	err := eng.Mount(app)
	if err == nil {
		t.Fatal("expected mount to fail without subject-key fn wired")
	}
	if !strings.Contains(err.Error(), "WithRateLimitSubjectKeyFn") {
		t.Errorf("err = %v, want mention of WithRateLimitSubjectKeyFn", err)
	}
}
