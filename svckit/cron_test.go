package svckit

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// cronWrapMod registers a WrapCronJob decorator during Setup so tests
// can verify the decorator actually fires — end to end through New,
// not just at the hostImpl/Service.wrapCronJob unit level (see
// TestHost_WrapCronJobAccumulatesInOrder in host_test.go for that).
type cronWrapMod struct {
	calls *int32
}

func (cronWrapMod) Name() string { return "cron_wrap" }

func (m cronWrapMod) Setup(_ context.Context, h Host) error {
	h.WrapCronJob(func(next JobFn) JobFn {
		return func(ctx context.Context) error {
			atomic.AddInt32(m.calls, 1)
			return next(ctx)
		}
	})
	return nil
}

// waitForCount polls got until it's > 0 or the deadline passes.
func waitForCount(t *testing.T, got *int32) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(got) > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for cron tick")
}

// TestAddCron_AppliesModRegisteredWrapper covers the post-build
// AddCron path: a mod's WrapCronJob decorator (registered during
// Setup) must run around every job AddCron schedules afterwards.
func TestAddCron_AppliesModRegisteredWrapper(t *testing.T) {
	var wrapCalls int32
	svc, err := New[struct{}, struct{}](context.Background(), Config{},
		WithMod(cronWrapMod{calls: &wrapCalls}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer svc.Close()

	var jobCalls int32
	if err := svc.AddCron("probe", "@every 20ms", func(ctx context.Context) error {
		atomic.AddInt32(&jobCalls, 1)
		return nil
	}); err != nil {
		t.Fatalf("AddCron: %v", err)
	}

	waitForCount(t, &jobCalls)
	if atomic.LoadInt32(&wrapCalls) == 0 {
		t.Error("mod-registered WrapCronJob decorator never ran — AddCron did not apply it")
	}
}

// TestWithCron_AppliesModRegisteredWrapper covers the config-time
// WithCron path, built inside New by buildCron — the other call site
// wrapCronJob needed to be threaded into.
func TestWithCron_AppliesModRegisteredWrapper(t *testing.T) {
	var wrapCalls, jobCalls int32
	svc, err := New[struct{}, struct{}](context.Background(), Config{},
		WithMod(cronWrapMod{calls: &wrapCalls}),
		WithCron("probe", "@every 20ms", func(ctx context.Context) error {
			atomic.AddInt32(&jobCalls, 1)
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer svc.Close()

	waitForCount(t, &jobCalls)
	if atomic.LoadInt32(&wrapCalls) == 0 {
		t.Error("mod-registered WrapCronJob decorator never ran — buildCron did not apply it")
	}
}
