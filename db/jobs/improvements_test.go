package jobs_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/theizzatbek/gokit/db/jobs"
	xerrs "github.com/theizzatbek/gokit/errs"
)

type opsPayload struct {
	UserID string `json:"user_id"`
}

// ── WithDedupKey ──────────────────────────────────────────────────

func TestWithDedupKey_SecondScheduleReturnsExistingID(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()

	id1, err := jobs.Schedule(ctx, d, time.Now().Add(time.Hour),
		"billing.invoice", opsPayload{UserID: "u-1"},
		jobs.WithDedupKey("u-1:2026-06"),
	)
	if err != nil {
		t.Fatal(err)
	}

	id2, err := jobs.Schedule(ctx, d, time.Now().Add(time.Hour),
		"billing.invoice", opsPayload{UserID: "u-1"},
		jobs.WithDedupKey("u-1:2026-06"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if id1 != id2 {
		t.Errorf("ids differ: id1=%d id2=%d (expected dedupe)", id1, id2)
	}

	// Only ONE row should exist.
	var count int
	if err := d.QueryRow(ctx,
		`SELECT count(*) FROM jobs WHERE type='billing.invoice' AND dedup_key='u-1:2026-06'`,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("row count = %d, want 1", count)
	}
}

func TestWithDedupKey_DifferentKeysInsertSeparate(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()

	id1, _ := jobs.Schedule(ctx, d, time.Now().Add(time.Hour),
		"billing.invoice", opsPayload{}, jobs.WithDedupKey("u-1:2026-06"))
	id2, _ := jobs.Schedule(ctx, d, time.Now().Add(time.Hour),
		"billing.invoice", opsPayload{}, jobs.WithDedupKey("u-2:2026-06"))

	if id1 == id2 {
		t.Errorf("different dedup keys must produce distinct ids")
	}
}

func TestWithDedupKey_ReusableAfterCompletion(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()

	id1, _ := jobs.Schedule(ctx, d, time.Now(),
		"task.daily", opsPayload{}, jobs.WithDedupKey("daily:2026-06-04"))

	// Simulate completion.
	if _, err := d.Exec(ctx,
		`UPDATE jobs SET state='done', finished_at=NOW() WHERE id=$1`, id1,
	); err != nil {
		t.Fatal(err)
	}

	// Same dedup key re-schedules cleanly (partial index doesn't see
	// done rows).
	id2, err := jobs.Schedule(ctx, d, time.Now(),
		"task.daily", opsPayload{}, jobs.WithDedupKey("daily:2026-06-04"))
	if err != nil {
		t.Fatal(err)
	}
	if id1 == id2 {
		t.Errorf("re-schedule after done should produce new id; got same %d", id1)
	}
}

// ── WithPriority ──────────────────────────────────────────────────

func TestWithPriority_HighPriorityClaimsFirst(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()

	idLow, _ := jobs.Schedule(ctx, d, time.Now().Add(-time.Hour),
		"task", opsPayload{UserID: "low"}, jobs.WithPriority(0))
	time.Sleep(5 * time.Millisecond) // ensure run_at diverges so a non-priority worker would pick low first
	idHigh, _ := jobs.Schedule(ctx, d, time.Now().Add(-time.Hour),
		"task", opsPayload{UserID: "high"}, jobs.WithPriority(100))

	var seen []string
	var mu sync.Mutex

	w, _ := jobs.NewWorker(d, jobs.WithInterval(20*time.Millisecond))
	jobs.RegisterHandler[opsPayload](w, "task", func(ctx context.Context, p opsPayload) error {
		mu.Lock()
		seen = append(seen, p.UserID)
		mu.Unlock()
		return nil
	})

	startCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() { _ = w.Start(startCtx) }()
	t.Cleanup(func() { _ = w.Stop() })

	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(seen) == 2
	}, 2*time.Second, "both jobs dispatched")

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 {
		t.Fatalf("seen = %v", seen)
	}
	if seen[0] != "high" {
		t.Errorf("first dispatched = %q, want 'high' (priority should win)", seen[0])
	}
	_ = idLow
	_ = idHigh
}

// ── Cancel ────────────────────────────────────────────────────────

func TestCancel_QueuedJob(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()

	id, _ := jobs.Schedule(ctx, d, time.Now().Add(time.Hour),
		"task.future", opsPayload{})

	if err := jobs.Cancel(ctx, d, id); err != nil {
		t.Fatal(err)
	}

	var state string
	if err := d.QueryRow(ctx,
		`SELECT state FROM jobs WHERE id = $1`, id,
	).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "cancelled" {
		t.Errorf("state = %q, want cancelled", state)
	}
}

func TestCancel_NonExistentReturnsNotFound(t *testing.T) {
	d := freshDB(t)
	err := jobs.Cancel(context.Background(), d, 99999)
	if err == nil {
		t.Fatal("expected not-found")
	}
	var e *xerrs.Error
	if !errors.As(err, &e) || e.Code != jobs.CodeJobNotFound {
		t.Errorf("err = %+v, want CodeJobNotFound", err)
	}
}

func TestCancel_AlreadyDoneReturnsNotFound(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()

	id, _ := jobs.Schedule(ctx, d, time.Now(), "task", opsPayload{})
	if _, err := d.Exec(ctx, `UPDATE jobs SET state='done' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}

	err := jobs.Cancel(ctx, d, id)
	if err == nil {
		t.Fatal("expected not-found on already-done row")
	}
}

func TestCancel_WorkerSkipsRow(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()

	id, _ := jobs.Schedule(ctx, d, time.Now().Add(-time.Hour),
		"task.cancelled", opsPayload{})
	if err := jobs.Cancel(ctx, d, id); err != nil {
		t.Fatal(err)
	}

	var ran int32
	w, _ := jobs.NewWorker(d, jobs.WithInterval(20*time.Millisecond))
	jobs.RegisterHandler[opsPayload](w, "task.cancelled", func(ctx context.Context, p opsPayload) error {
		atomic.AddInt32(&ran, 1)
		return nil
	})

	startCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() { _ = w.Start(startCtx) }()
	t.Cleanup(func() { _ = w.Stop() })

	time.Sleep(300 * time.Millisecond)

	if atomic.LoadInt32(&ran) != 0 {
		t.Errorf("cancelled job was dispatched (ran=%d)", atomic.LoadInt32(&ran))
	}
}

// ── Stats ─────────────────────────────────────────────────────────

func TestGatherStats_EmptyTable(t *testing.T) {
	d := freshDB(t)
	s, err := jobs.GatherStats(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	if s.Queued != 0 || s.Running != 0 || s.Failed != 0 || s.Done != 0 || s.Cancelled != 0 {
		t.Errorf("stats not zero: %+v", s)
	}
}

func TestGatherStats_MixedStates(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()

	// 2 queued (one eligible, one future).
	idElig, _ := jobs.Schedule(ctx, d, time.Now().Add(-time.Hour), "t1", opsPayload{})
	idFuture, _ := jobs.Schedule(ctx, d, time.Now().Add(time.Hour), "t2", opsPayload{})
	_ = idElig

	// 1 running.
	idR, _ := jobs.Schedule(ctx, d, time.Now(), "t3", opsPayload{})
	if _, err := d.Exec(ctx, `UPDATE jobs SET state='running' WHERE id=$1`, idR); err != nil {
		t.Fatal(err)
	}

	// 1 done.
	idD, _ := jobs.Schedule(ctx, d, time.Now(), "t4", opsPayload{})
	if _, err := d.Exec(ctx, `UPDATE jobs SET state='done', finished_at=NOW() WHERE id=$1`, idD); err != nil {
		t.Fatal(err)
	}

	// 1 cancelled.
	idC, _ := jobs.Schedule(ctx, d, time.Now(), "t5", opsPayload{})
	if err := jobs.Cancel(ctx, d, idC); err != nil {
		t.Fatal(err)
	}

	s, err := jobs.GatherStats(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	if s.Queued != 2 {
		t.Errorf("Queued = %d, want 2", s.Queued)
	}
	if s.Eligible != 1 {
		t.Errorf("Eligible = %d, want 1", s.Eligible)
	}
	if s.Running != 1 {
		t.Errorf("Running = %d, want 1", s.Running)
	}
	if s.Done != 1 {
		t.Errorf("Done = %d, want 1", s.Done)
	}
	if s.Cancelled != 1 {
		t.Errorf("Cancelled = %d, want 1", s.Cancelled)
	}
	if s.OldestQueued.IsZero() {
		t.Errorf("OldestQueued unset")
	}
	_ = idFuture
}

// ── Shutdown ──────────────────────────────────────────────────────

func TestShutdown_CleanExit(t *testing.T) {
	d := freshDB(t)
	w, _ := jobs.NewWorker(d, jobs.WithInterval(50*time.Millisecond))
	startCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = w.Start(startCtx) }()

	// Let the loop spin up.
	time.Sleep(100 * time.Millisecond)

	shutdownCtx, sCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer sCancel()
	if err := w.Shutdown(shutdownCtx); err != nil {
		t.Errorf("Shutdown err = %v, want nil", err)
	}
}

func TestShutdown_RespectsDeadlineWhenHandlerStuck(t *testing.T) {
	d := freshDB(t)
	ctx := context.Background()

	_, _ = jobs.Schedule(ctx, d, time.Now(), "task.stuck", opsPayload{})

	released := make(chan struct{})
	w, _ := jobs.NewWorker(d, jobs.WithInterval(20*time.Millisecond))
	jobs.RegisterHandler[opsPayload](w, "task.stuck", func(ctx context.Context, p opsPayload) error {
		<-released
		return nil
	})

	startCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() { _ = w.Start(startCtx) }()

	// Let the worker claim + start the stuck handler.
	time.Sleep(200 * time.Millisecond)

	shutdownCtx, sCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer sCancel()
	err := w.Shutdown(shutdownCtx)
	if err == nil {
		t.Fatal("expected ctx deadline error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want DeadlineExceeded", err)
	}

	// Release the stuck handler so the loop can finish — otherwise
	// the test process leaks the goroutine. After release, Stop
	// drains cleanly.
	close(released)
	_ = w.Stop()
}

func TestShutdown_IdempotentWithStop(t *testing.T) {
	d := freshDB(t)
	w, _ := jobs.NewWorker(d, jobs.WithInterval(50*time.Millisecond))
	startCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = w.Start(startCtx) }()
	time.Sleep(100 * time.Millisecond)

	shutdownCtx, sCancel := context.WithTimeout(context.Background(), time.Second)
	defer sCancel()
	if err := w.Shutdown(shutdownCtx); err != nil {
		t.Fatal(err)
	}
	if err := w.Stop(); err != nil {
		t.Errorf("Stop after Shutdown should be nil; got %v", err)
	}
}

// silence unused import on platforms where time import isn't enough
var _ = time.Millisecond
