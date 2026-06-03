package cache_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/theizzatbek/gokit/clients/cache"
)

// ── B. GetOrLoad ───────────────────────────────────────────────────────

func TestGetOrLoad_LoaderHit_PopulatesPositive(t *testing.T) {
	if testRDB == nil {
		t.Skip("requires Redis container")
	}
	c, err := cache.New[payload](testRDB, cache.Config{
		KeyPrefix:   "imp:hit:",
		PositiveTTL: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	var loaderCalls atomic.Int32
	loader := func(ctx context.Context, key string) (payload, bool, error) {
		loaderCalls.Add(1)
		return payload{ID: key, Name: "loaded"}, true, nil
	}

	hit, err := c.GetOrLoad(context.Background(), "k1", loader)
	if err != nil {
		t.Fatal(err)
	}
	if hit.Value == nil || hit.Value.Name != "loaded" {
		t.Fatalf("first call hit = %+v", hit)
	}

	// Second call — should hit cache, NOT loader.
	hit2, _ := c.GetOrLoad(context.Background(), "k1", loader)
	if hit2.Value == nil || hit2.Value.Name != "loaded" {
		t.Fatalf("second call hit = %+v", hit2)
	}
	if got := loaderCalls.Load(); got != 1 {
		t.Errorf("loader calls = %d, want 1", got)
	}
}

func TestGetOrLoad_LoaderMiss_PopulatesNegative(t *testing.T) {
	if testRDB == nil {
		t.Skip("requires Redis container")
	}
	c, err := cache.New[payload](testRDB, cache.Config{
		KeyPrefix:   "imp:neg:",
		PositiveTTL: 5 * time.Second,
		NegativeTTL: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	var loaderCalls atomic.Int32
	loader := func(ctx context.Context, key string) (payload, bool, error) {
		loaderCalls.Add(1)
		return payload{}, false, nil
	}
	hit, _ := c.GetOrLoad(context.Background(), "k1", loader)
	if !hit.NotFound {
		t.Fatalf("first call = %+v, want NotFound", hit)
	}
	// Negative cached now — second call must NOT hit loader.
	hit2, _ := c.GetOrLoad(context.Background(), "k1", loader)
	if !hit2.NotFound {
		t.Fatalf("second call = %+v, want NotFound", hit2)
	}
	if got := loaderCalls.Load(); got != 1 {
		t.Errorf("loader calls = %d, want 1 (negative cached)", got)
	}
}

func TestGetOrLoad_LoaderError_DoesNotCache(t *testing.T) {
	if testRDB == nil {
		t.Skip("requires Redis container")
	}
	c, _ := cache.New[payload](testRDB, cache.Config{KeyPrefix: "imp:err:"})

	want := errors.New("transient")
	var calls atomic.Int32
	loader := func(context.Context, string) (payload, bool, error) {
		calls.Add(1)
		return payload{}, false, want
	}
	_, err := c.GetOrLoad(context.Background(), "k1", loader)
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
	// Loader err must NOT be cached — second call retries.
	_, _ = c.GetOrLoad(context.Background(), "k1", loader)
	if calls.Load() != 2 {
		t.Errorf("calls = %d, want 2", calls.Load())
	}
}

func TestGetOrLoad_SingleFlight_FoldsConcurrent(t *testing.T) {
	if testRDB == nil {
		t.Skip("requires Redis container")
	}
	c, _ := cache.New[payload](testRDB, cache.Config{KeyPrefix: "imp:sf:"})

	var calls atomic.Int32
	loader := func(ctx context.Context, key string) (payload, bool, error) {
		calls.Add(1)
		time.Sleep(50 * time.Millisecond) // hold the in-flight slot
		return payload{ID: key, Name: "loaded"}, true, nil
	}

	const concurrent = 20
	results := make(chan cache.Lookup[payload], concurrent)
	for i := 0; i < concurrent; i++ {
		go func() {
			hit, _ := c.GetOrLoad(context.Background(), "stampede-key", loader)
			results <- hit
		}()
	}
	for i := 0; i < concurrent; i++ {
		hit := <-results
		if hit.Value == nil {
			t.Errorf("hit %d: value nil", i)
		}
	}
	// Single-flight collapses N concurrent goroutines into 1 loader.
	if got := calls.Load(); got != 1 {
		t.Errorf("loader calls = %d, want 1 (single-flight)", got)
	}
}

// ── C. Metrics ─────────────────────────────────────────────────────────

func TestMetrics_RecordHitMiss(t *testing.T) {
	if testRDB == nil {
		t.Skip("requires Redis container")
	}
	reg := prometheus.NewRegistry()
	c, err := cache.New[payload](testRDB, cache.Config{
		KeyPrefix:  "imp:m:",
		Name:       "test-cache",
		MetricsReg: reg,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Miss + Set + Hit.
	_ = c.Get(context.Background(), "missing")
	c.Set(context.Background(), "k1", payload{ID: "k1"})
	_ = c.Get(context.Background(), "k1")

	got := counterValue(t, reg, "cache_operations_total", "test-cache", "get", "hit")
	if got != 1 {
		t.Errorf("get hit = %v, want 1", got)
	}
	if v := counterValue(t, reg, "cache_operations_total", "test-cache", "get", "miss"); v != 1 {
		t.Errorf("get miss = %v, want 1", v)
	}
	if v := counterValue(t, reg, "cache_operations_total", "test-cache", "set", "ok"); v != 1 {
		t.Errorf("set ok = %v, want 1", v)
	}
}

func TestMetrics_RequireName(t *testing.T) {
	reg := prometheus.NewRegistry()
	_, err := cache.New[payload](nil, cache.Config{
		KeyPrefix:  "imp:noname:",
		MetricsReg: reg,
	})
	if err == nil {
		t.Fatal("expected error: Name required with MetricsReg")
	}
}

// counterValue walks the registry for a counter labelled
// {name, operation, outcome}.
func counterValue(t *testing.T, reg *prometheus.Registry, family, name, op, outcome string) float64 {
	t.Helper()
	mfs, _ := reg.Gather()
	for _, mf := range mfs {
		if mf.GetName() != family {
			continue
		}
		for _, m := range mf.Metric {
			var n, o, oc string
			for _, l := range m.GetLabel() {
				switch l.GetName() {
				case "name":
					n = l.GetValue()
				case "operation":
					o = l.GetValue()
				case "outcome":
					oc = l.GetValue()
				}
			}
			if n == name && o == op && oc == outcome {
				return m.GetCounter().GetValue()
			}
		}
	}
	return 0
}

// ── D. TTL jitter ──────────────────────────────────────────────────────

func TestTTLJitter_VariesEffectiveTTL(t *testing.T) {
	if testRDB == nil {
		t.Skip("requires Redis container")
	}
	c, _ := cache.New[payload](testRDB, cache.Config{
		KeyPrefix:   "imp:j:",
		PositiveTTL: 100 * time.Second,
		TTLJitter:   0.5, // wide enough to clearly diverge
	})
	c.Set(context.Background(), "k1", payload{ID: "k1"})
	c.Set(context.Background(), "k2", payload{ID: "k2"})

	t1, err := testRDB.PTTL(context.Background(), "imp:j:k1").Result()
	if err != nil {
		t.Fatal(err)
	}
	t2, err := testRDB.PTTL(context.Background(), "imp:j:k2").Result()
	if err != nil {
		t.Fatal(err)
	}
	if t1 == t2 {
		t.Errorf("TTL jitter expected variance; both = %v", t1)
	}
	// Loose bounds: ±50% from 100s.
	if t1 < 50*time.Second || t1 > 150*time.Second {
		t.Errorf("t1 = %v outside ±50%% of 100s", t1)
	}
}

// ── E. Custom codec ──────────────────────────────────────────────────────

type uppercaseCodec struct{}

func (uppercaseCodec) Marshal(v any) ([]byte, error) {
	p := v.(payload)
	return []byte("U:" + p.ID), nil
}
func (uppercaseCodec) Unmarshal(b []byte, v any) error {
	p := v.(*payload)
	s := string(b)
	if len(s) > 2 && s[:2] == "U:" {
		p.ID = s[2:]
		return nil
	}
	return errors.New("not uppercase format")
}

func TestWithCodec_RoundTrip(t *testing.T) {
	if testRDB == nil {
		t.Skip("requires Redis container")
	}
	c, _ := cache.New[payload](testRDB, cache.Config{
		KeyPrefix: "imp:codec:",
		Codec:     uppercaseCodec{},
	})
	c.Set(context.Background(), "k1", payload{ID: "abc"})
	got := c.Get(context.Background(), "k1")
	if got.Value == nil || got.Value.ID != "abc" {
		t.Fatalf("round trip failed: %+v", got)
	}

	// Verify the on-wire encoding matches the custom codec.
	raw, _ := testRDB.Get(context.Background(), "imp:codec:k1").Result()
	if raw != "U:abc" {
		t.Errorf("on-wire = %q, want U:abc", raw)
	}
}

// ── H. InvalidatePrefix ────────────────────────────────────────────────

func TestInvalidatePrefix_DropsMatching(t *testing.T) {
	if testRDB == nil {
		t.Skip("requires Redis container")
	}
	c, _ := cache.New[payload](testRDB, cache.Config{
		KeyPrefix:   "imp:inv:",
		PositiveTTL: time.Minute,
	})
	c.Set(context.Background(), "tenant-a:1", payload{ID: "a1"})
	c.Set(context.Background(), "tenant-a:2", payload{ID: "a2"})
	c.Set(context.Background(), "tenant-b:1", payload{ID: "b1"})

	c.InvalidatePrefix(context.Background(), "tenant-a:")

	if hit := c.Get(context.Background(), "tenant-a:1"); hit.Value != nil {
		t.Errorf("tenant-a:1 still cached: %+v", hit)
	}
	if hit := c.Get(context.Background(), "tenant-b:1"); hit.Value == nil {
		t.Errorf("tenant-b:1 was dropped — InvalidatePrefix over-deleted")
	}
}
