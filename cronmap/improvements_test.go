package cronmap

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	xerrs "github.com/theizzatbek/gokit/errs"
)

// ── A. Retry policy ─────────────────────────────────────────────────

func TestRuntime_RetrySucceedsOnNthAttempt(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32

	e := newTestEngineWithJob(t, "rt",
		func(context.Context) error {
			n := calls.Add(1)
			if n < 3 {
				return errors.New("transient")
			}
			return nil
		},
		"max_retries: 5",
		"retry_backoff: 5ms",
	)
	rt, err := e.Build(WithParser(secondPrecisionParser()))
	if err != nil {
		t.Fatal(err)
	}

	// Force-fire so we don't wait for the cron tick.
	if err := rt.TriggerJob(context.Background(), "rt", OverrideOK{}); err != nil {
		t.Fatal(err)
	}

	if got := calls.Load(); got != 3 {
		t.Errorf("calls = %d, want 3 (3rd attempt succeeds)", got)
	}
	stats := findStats(rt.Stats(), "rt")
	if stats.SuccessCount != 1 {
		t.Errorf("SuccessCount = %d, want 1", stats.SuccessCount)
	}
	if stats.FailureCount != 0 {
		t.Errorf("FailureCount = %d, want 0 (retry succeeded)", stats.FailureCount)
	}
}

func TestRuntime_RetryExhaustsAndReportsFailure(t *testing.T) {
	t.Parallel()
	want := errors.New("always-fails")
	var calls atomic.Int32

	e := newTestEngineWithJob(t, "exhausted",
		func(context.Context) error {
			calls.Add(1)
			return want
		},
		"max_retries: 2",
		"retry_backoff: 1ms",
	)
	rt, err := e.Build(WithParser(secondPrecisionParser()))
	if err != nil {
		t.Fatal(err)
	}

	if err := rt.TriggerJob(context.Background(), "exhausted", OverrideOK{}); err != nil {
		t.Fatal(err)
	}

	if got := calls.Load(); got != 3 {
		t.Errorf("calls = %d, want 3 (initial + 2 retries)", got)
	}
	stats := findStats(rt.Stats(), "exhausted")
	if stats.FailureCount != 1 {
		t.Errorf("FailureCount = %d, want 1", stats.FailureCount)
	}
}

// ── B. Hooks ───────────────────────────────────────────────────────

func TestRuntime_OnTickStartCompleteFire(t *testing.T) {
	t.Parallel()
	var startCalls, completeCalls atomic.Int32
	var lastErrMu sync.Mutex
	var lastErr error

	e := newTestEngineWithJob(t, "hk",
		func(context.Context) error { return errors.New("boom") })

	rt, err := e.Build(
		WithParser(secondPrecisionParser()),
		WithOnTickStart(func(_ context.Context, _ string) {
			startCalls.Add(1)
		}),
		WithOnTickComplete(func(_ context.Context, _ string, err error, _ time.Duration) {
			completeCalls.Add(1)
			lastErrMu.Lock()
			lastErr = err
			lastErrMu.Unlock()
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	_ = rt.TriggerJob(context.Background(), "hk", OverrideOK{})

	if startCalls.Load() != 1 {
		t.Errorf("OnTickStart fires = %d, want 1", startCalls.Load())
	}
	if completeCalls.Load() != 1 {
		t.Errorf("OnTickComplete fires = %d, want 1", completeCalls.Load())
	}
	lastErrMu.Lock()
	defer lastErrMu.Unlock()
	if lastErr == nil {
		t.Error("OnTickComplete err = nil, want non-nil (handler returns err)")
	}
}

func TestRuntime_HookPanicRecovered(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("panic escaped runtime: %v", r)
		}
	}()
	e := newTestEngineWithJob(t, "pn",
		func(context.Context) error { return nil })
	rt, _ := e.Build(
		WithParser(secondPrecisionParser()),
		WithOnTickStart(func(context.Context, string) { panic("hook boom") }),
	)
	_ = rt.TriggerJob(context.Background(), "pn", OverrideOK{})
}

// ── C. Stats ───────────────────────────────────────────────────────

func TestRuntime_Stats_ReportsCounters(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32

	e := newTestEngineWithJob(t, "st",
		func(context.Context) error {
			calls.Add(1)
			return nil
		})
	rt, err := e.Build(WithParser(secondPrecisionParser()))
	if err != nil {
		t.Fatal(err)
	}

	// Trigger twice.
	_ = rt.TriggerJob(context.Background(), "st", OverrideOK{})
	_ = rt.TriggerJob(context.Background(), "st", OverrideOK{})

	stats := findStats(rt.Stats(), "st")
	if stats.TotalRuns != 2 {
		t.Errorf("TotalRuns = %d, want 2", stats.TotalRuns)
	}
	if stats.SuccessCount != 2 {
		t.Errorf("SuccessCount = %d, want 2", stats.SuccessCount)
	}
	if stats.LastRunAt.IsZero() {
		t.Error("LastRunAt zero — stats not recording")
	}
	if stats.NextRunAt.IsZero() {
		t.Error("NextRunAt zero — schedule.Next not invoked")
	}
}

// ── DF. TriggerJob + NextRun ───────────────────────────────────────

func TestRuntime_TriggerJob_RunsSync(t *testing.T) {
	t.Parallel()
	var ran atomic.Bool
	e := newTestEngineWithJob(t, "trg",
		func(context.Context) error {
			ran.Store(true)
			return nil
		})
	rt, _ := e.Build(WithParser(secondPrecisionParser()))

	if err := rt.TriggerJob(context.Background(), "trg", OverrideOK{}); err != nil {
		t.Fatal(err)
	}
	if !ran.Load() {
		t.Error("handler did not run synchronously")
	}
}

func TestRuntime_TriggerJob_UnknownName(t *testing.T) {
	t.Parallel()
	e := newTestEngineWithJob(t, "knownj",
		func(context.Context) error { return nil })
	rt, _ := e.Build(WithParser(secondPrecisionParser()))

	err := rt.TriggerJob(context.Background(), "ghost", OverrideOK{})
	if err == nil {
		t.Fatal("expected NotFound err")
	}
	var e2 *xerrs.Error
	if !errors.As(err, &e2) || e2.Code != CodeUnknownJob {
		t.Errorf("err code = %v, want %q", err, CodeUnknownJob)
	}
}

func TestRuntime_NextRun_ReturnsFuture(t *testing.T) {
	t.Parallel()
	e := newTestEngineWithJob(t, "nx",
		func(context.Context) error { return nil })
	rt, _ := e.Build(WithParser(secondPrecisionParser()))

	next, err := rt.NextRun("nx")
	if err != nil {
		t.Fatal(err)
	}
	if !next.After(time.Now()) {
		t.Errorf("NextRun = %v, want > now (%v)", next, time.Now())
	}
}

// ── E. Pause/Resume ───────────────────────────────────────────────

func TestRuntime_PauseResumeJob_TickSkipsAndRecovers(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	e := newTestEngineWithJob(t, "pr",
		func(context.Context) error {
			calls.Add(1)
			return nil
		})
	rt, _ := e.Build(WithParser(secondPrecisionParser()))

	if err := rt.PauseJob("pr"); err != nil {
		t.Fatal(err)
	}

	// Use runOnce directly so the paused guard fires deterministically
	// (tick adds a runCtx-nil bail-out which we don't want here).
	// skipSingleton=false routes through the pause check.
	rt.runOnce(context.Background(), rt.jobs[0], false)
	if got := calls.Load(); got != 0 {
		t.Errorf("calls = %d, want 0 (paused — tick should skip)", got)
	}
	stats := findStats(rt.Stats(), "pr")
	if stats.SkippedCount != 1 {
		t.Errorf("SkippedCount = %d, want 1 (paused skip recorded)", stats.SkippedCount)
	}

	// Resume → next runOnce runs.
	if err := rt.ResumeJob("pr"); err != nil {
		t.Fatal(err)
	}
	rt.runOnce(context.Background(), rt.jobs[0], false)
	if got := calls.Load(); got == 0 {
		t.Error("handler did not run after Resume")
	}
}

func TestRuntime_PauseJob_UnknownName(t *testing.T) {
	t.Parallel()
	e := newTestEngineWithJob(t, "p1",
		func(context.Context) error { return nil })
	rt, _ := e.Build(WithParser(secondPrecisionParser()))

	err := rt.PauseJob("ghost")
	if err == nil {
		t.Fatal("expected NotFound")
	}
	if !strings.Contains(err.Error(), CodeUnknownJob) {
		t.Errorf("err = %v, want %q", err, CodeUnknownJob)
	}
}

// ── helpers ─────────────────────────────────────────────────────────

func findStats(all []JobStats, name string) JobStats {
	for _, s := range all {
		if s.Name == name {
			return s
		}
	}
	return JobStats{}
}
