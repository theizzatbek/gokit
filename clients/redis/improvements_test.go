package redisclient

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"

	"github.com/theizzatbek/gokit/breaker"
	xerrs "github.com/theizzatbek/gokit/errs"
)

// ── AB. Single-mode Client wired through UniversalClient ───────────────

func TestClient_Mode_ReportsSingle(t *testing.T) {
	if testURL == "" {
		t.Skip("requires Redis container")
	}
	c, err := Connect(context.Background(), Config{URL: testURL})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })

	if got := c.Mode(); got != ModeSingle {
		t.Errorf("Mode = %v, want single", got)
	}
	if c.Redis() == nil {
		t.Error("Redis() must return *redis.Client in single mode")
	}
	if c.Universal() == nil {
		t.Error("Universal() must return non-nil in any mode")
	}
}

// ── C. WithHook composability ──────────────────────────────────────────

type countingHook struct {
	hits atomic.Int32
}

func (h *countingHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *countingHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		h.hits.Add(1)
		return next(ctx, cmd)
	}
}
func (h *countingHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		h.hits.Add(1)
		return next(ctx, cmds)
	}
}

func TestWithHook_RunsAlongsideKitHook(t *testing.T) {
	if testURL == "" {
		t.Skip("requires Redis container")
	}
	h := &countingHook{}
	c, err := Connect(context.Background(), Config{URL: testURL}, WithHook(h))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })

	// One PING here = one ProcessHook invocation on the user hook.
	if err := c.Universal().Ping(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	if h.hits.Load() < 1 {
		t.Errorf("user hook hits = %d, want >= 1", h.hits.Load())
	}
}

// ── D. ConnectionStatus gauge ──────────────────────────────────────────

func TestConnectionStatus_GaugeSetOnConnectClearedOnClose(t *testing.T) {
	if testURL == "" {
		t.Skip("requires Redis container")
	}
	reg := prometheus.NewRegistry()
	c, err := Connect(context.Background(), Config{URL: testURL}, WithMetrics(reg))
	if err != nil {
		t.Fatal(err)
	}

	gather := func() float64 {
		mfs, _ := reg.Gather()
		for _, mf := range mfs {
			if mf.GetName() == "redis_connection_status" {
				if len(mf.Metric) > 0 {
					return mf.Metric[0].GetGauge().GetValue()
				}
			}
		}
		return -1
	}
	if got := gather(); got != 1 {
		t.Errorf("status post-Connect = %v, want 1", got)
	}
	_ = c.Close()
	if got := gather(); got != 0 {
		t.Errorf("status post-Close = %v, want 0", got)
	}
}

// ── E. WithDefaultTimeout ──────────────────────────────────────────────

func TestWithDefaultTimeout_AppliesWhenNoDeadline(t *testing.T) {
	if testURL == "" {
		t.Skip("requires Redis container")
	}
	// Build a non-existent Redis URL so commands hang until the
	// kit timeout cuts in. We can't pretend with the live test
	// container — instead test the hook in isolation by reading
	// the derived ctx's deadline.
	h := &hook{defaultTimeout: 50 * time.Millisecond}

	ctx, cancel := h.deriveTimeoutCtx(context.Background())
	if cancel == nil {
		t.Fatal("expected cancel non-nil when no caller deadline")
	}
	defer cancel()
	if _, has := ctx.Deadline(); !has {
		t.Error("derived ctx must have a deadline")
	}

	// With caller deadline set already: hook must NOT wrap it.
	parent, parentCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer parentCancel()
	ctx2, cancel2 := h.deriveTimeoutCtx(parent)
	if cancel2 != nil {
		t.Error("hook must NOT wrap a ctx that already has a deadline")
	}
	if ctx2 != parent {
		t.Error("ctx must pass through unchanged when deadline already set")
	}
}

// ── I. WithBreaker ─────────────────────────────────────────────────────

func TestWithBreaker_ExecuteWithBreaker_OpenWraps(t *testing.T) {
	t.Parallel()
	br, err := breaker.New(breaker.Config{
		Name:             "redis-unit",
		FailureThreshold: 1,
		MinimumRequests:  1,
		OpenInterval:     time.Hour, // hold the open state
	})
	if err != nil {
		t.Fatal(err)
	}

	h := &hook{breaker: br}

	// First call fails → trips the breaker.
	fakeFail := errors.New("upstream boom")
	if got := h.executeWithBreaker(func() error { return fakeFail }); !errors.Is(got, fakeFail) {
		t.Fatalf("first call err = %v, want fakeFail", got)
	}

	// Subsequent call short-circuits — surfaces as *errs.Error with
	// CodeCircuitOpen wrapping breaker.ErrOpen.
	got := h.executeWithBreaker(func() error {
		t.Error("inner fn must not run after breaker open")
		return nil
	})
	if !errors.Is(got, breaker.ErrOpen) {
		t.Errorf("got = %v, want ErrOpen via errors.Is", got)
	}
	var e *xerrs.Error
	if !errors.As(got, &e) || e.Code != CodeCircuitOpen {
		t.Errorf("err = %v, want CodeCircuitOpen", got)
	}
}

func TestWithBreaker_RedisNilCountsAsSuccess(t *testing.T) {
	t.Parallel()
	br, err := breaker.New(breaker.Config{
		Name:             "redis-nil-test",
		FailureThreshold: 1,
		MinimumRequests:  1,
		OpenInterval:     time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	h := &hook{breaker: br}

	// redis.Nil must NOT trip the breaker — it's the "key not found"
	// signal, not an operational failure.
	for i := 0; i < 5; i++ {
		got := h.executeWithBreaker(func() error { return redis.Nil })
		if !errors.Is(got, redis.Nil) {
			t.Fatalf("redis.Nil must pass through, got %v", got)
		}
	}
	// Now a real failure must still surface unchanged (breaker is
	// still closed — redis.Nil didn't count as fail).
	real := errors.New("real")
	if got := h.executeWithBreaker(func() error { return real }); !errors.Is(got, real) {
		t.Errorf("real err = %v, want real", got)
	}
}
