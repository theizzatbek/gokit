package cronmap

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/robfig/cron/v3"

	xerrs "github.com/theizzatbek/gokit/errs"
)

func newTestEngineWithJob(t *testing.T, name string, handler HandlerFn, extra ...string) *Engine {
	t.Helper()
	e := New()
	yaml := "jobs:\n  - name: " + name + "\n    handler: " + name + "\n    schedule: \"* * * * * *\"\n"
	for _, line := range extra {
		yaml += "    " + line + "\n"
	}
	if err := e.LoadBytes([]byte(yaml)); err != nil {
		t.Fatal(err)
	}
	RegisterHandler(e, name, handler)
	return e
}

func secondPrecisionParser() cron.Parser {
	return cron.NewParser(
		cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
	)
}

func TestRuntime_NilReceiverIsNoop(t *testing.T) {
	t.Parallel()
	var r *Runtime
	if err := r.Start(context.Background()); err != nil {
		t.Errorf("nil Start: %v", err)
	}
	if err := r.Stop(context.Background()); err != nil {
		t.Errorf("nil Stop: %v", err)
	}
	if got := r.JobNames(); got != nil {
		t.Errorf("nil JobNames = %v, want nil", got)
	}
}

func TestRuntime_StartStop_Lifecycle(t *testing.T) {
	t.Parallel()
	var hits atomic.Int64
	e := newTestEngineWithJob(t, "tick",
		func(context.Context) error { hits.Add(1); return nil })
	rt, err := e.Build(WithParser(secondPrecisionParser()))
	if err != nil {
		t.Fatal(err)
	}

	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if hits.Load() >= 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if hits.Load() < 1 {
		t.Fatalf("handler never ran; hits = %d", hits.Load())
	}

	if err := rt.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// Stop after Start sets stopped — second Start must reject.
	if err := rt.Start(context.Background()); err == nil ||
		!strings.Contains(err.Error(), CodeRuntimeStopped) {
		t.Errorf("Start after Stop = %v, want CodeRuntimeStopped", err)
	}
}

func TestRuntime_HandlerObservesRunCtxOnStop(t *testing.T) {
	t.Parallel()
	started := make(chan struct{}, 1)
	canceled := make(chan struct{}, 1)

	e := newTestEngineWithJob(t, "long", func(ctx context.Context) error {
		select {
		case started <- struct{}{}:
		default:
		}
		<-ctx.Done()
		select {
		case canceled <- struct{}{}:
		default:
		}
		return ctx.Err()
	})
	rt, err := e.Build(WithParser(secondPrecisionParser()))
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("handler never started")
	}

	if err := rt.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	select {
	case <-canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("handler ctx was not cancelled within 2s of Stop")
	}
}

func TestRuntime_TimeoutFiresMetric(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	e := newTestEngineWithJob(t, "slow",
		func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		},
		"timeout: 100ms",
	)
	rt, err := e.Build(WithParser(secondPrecisionParser()), WithMetrics(reg))
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Stop(context.Background()) })

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		got := testutil.ToFloat64(rt.collectors.runs.WithLabelValues("slow", outcomeTimeout))
		if got >= 1 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timeout outcome counter = %v, want >= 1",
		testutil.ToFloat64(rt.collectors.runs.WithLabelValues("slow", outcomeTimeout)))
}

func TestRuntime_SingletonSkipFiresSeparateMetric(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	var hits atomic.Int64
	e := newTestEngineWithJob(t, "leader",
		func(context.Context) error { hits.Add(1); return nil },
		"singleton: true",
	)
	deny := &countingLocker{denyFirst: 3}
	rt, err := e.Build(
		WithParser(secondPrecisionParser()),
		WithSingletonLocker(deny),
		WithMetrics(reg),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Stop(context.Background()) })

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		skips := testutil.ToFloat64(rt.collectors.singletonSkips.WithLabelValues("leader"))
		if skips >= 3 && hits.Load() >= 1 {
			// Skipped 3 times, then successfully acquired at least once.
			if rs := testutil.ToFloat64(rt.collectors.runs.WithLabelValues("leader", outcomeSuccess)); rs < 1 {
				t.Errorf("success counter = %v, want >= 1", rs)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("expected >=3 skips + >=1 success; skips=%v hits=%v",
		testutil.ToFloat64(rt.collectors.singletonSkips.WithLabelValues("leader")), hits.Load())
}

func TestRuntime_PanicBecomesFailure(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	e := newTestEngineWithJob(t, "boom",
		func(context.Context) error { panic("boom") })
	rt, err := e.Build(WithParser(secondPrecisionParser()), WithMetrics(reg))
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Stop(context.Background()) })

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		got := testutil.ToFloat64(rt.collectors.runs.WithLabelValues("boom", outcomeFailure))
		if got >= 1 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("failure outcome counter = %v, want >= 1",
		testutil.ToFloat64(rt.collectors.runs.WithLabelValues("boom", outcomeFailure)))
}

func TestRuntime_LockerErrorBecomesFailure(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	var hits atomic.Int64
	e := newTestEngineWithJob(t, "leader",
		func(context.Context) error { hits.Add(1); return nil },
		"singleton: true",
	)
	bad := &errLocker{err: errors.New("backend down")}
	rt, err := e.Build(
		WithParser(secondPrecisionParser()),
		WithSingletonLocker(bad),
		WithMetrics(reg),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Stop(context.Background()) })

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		got := testutil.ToFloat64(rt.collectors.runs.WithLabelValues("leader", outcomeFailure))
		if got >= 1 {
			if hits.Load() != 0 {
				t.Errorf("handler ran despite locker err; hits = %v", hits.Load())
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("failure outcome counter = %v, want >= 1",
		testutil.ToFloat64(rt.collectors.runs.WithLabelValues("leader", outcomeFailure)))
}

func TestRuntime_StopBeforeStart_Noop(t *testing.T) {
	t.Parallel()
	e := newTestEngineWithJob(t, "x", nopHandler)
	rt, err := e.Build(WithParser(secondPrecisionParser()))
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Stop(context.Background()); err != nil {
		t.Errorf("Stop before Start: %v", err)
	}
	// After Stop the runtime is sealed; Start must reject.
	if err := rt.Start(context.Background()); err == nil ||
		!strings.Contains(err.Error(), CodeRuntimeStopped) {
		t.Errorf("Start after stop-before-start = %v, want CodeRuntimeStopped", err)
	}
}

func TestPGLocker_NilDB_ReturnsNil(t *testing.T) {
	t.Parallel()
	if got := PGLocker(nil); got != nil {
		t.Errorf("PGLocker(nil) = %v, want nil", got)
	}
}

// countingLocker denies the first N TryLock calls (returns ok=false,
// nil err), then permits the rest. Used to verify singleton skip
// metric.
type countingLocker struct {
	mu        sync.Mutex
	denyFirst int
	calls     int
}

func (l *countingLocker) TryLock(_ context.Context, _ string) (func(), bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls++
	if l.calls <= l.denyFirst {
		return nil, false, nil
	}
	return func() {}, true, nil
}

// errLocker always returns a backend error.
type errLocker struct{ err error }

func (l *errLocker) TryLock(_ context.Context, _ string) (func(), bool, error) {
	return nil, false, l.err
}

// guard ensures xerrs is referenced even if test imports drift.
var _ = xerrs.KindInternal
