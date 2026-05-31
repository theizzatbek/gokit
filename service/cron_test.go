package service

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/robfig/cron/v3"
)

func newServiceForCronTest(t *testing.T, opts ...Option) *Service[map[string]any, any] {
	t.Helper()
	cfg := Config{}
	cfg.Service.LogLevel = "error"
	svc, err := New[map[string]any, any](context.Background(), cfg, opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(svc.Close)
	return svc
}

func TestSlugify_CommonShapes(t *testing.T) {
	cases := map[string]string{
		"daily-rollup":  "daily-rollup",
		"Daily Rollup":  "daily-rollup",
		"Orders/Sync":   "orders-sync",
		"  spaces  ":    "spaces",
		"!!!":           "job",
		"weekly_report": "weekly-report",
	}
	for in, want := range cases {
		got := slugify(in)
		if got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCron_NoJobs_BuildNoOp(t *testing.T) {
	svc := newServiceForCronTest(t)
	if svc.scheduler != nil {
		t.Errorf("scheduler should be nil without WithCron jobs; got %+v", svc.scheduler)
	}
}

func TestCron_SecondLevelTick_FiresJob(t *testing.T) {
	// Use the second-precision parser so we can drive a tight test.
	var hits int32
	svc := newServiceForCronTest(t,
		WithCronParser(cron.NewParser(cron.Second|cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow)),
		WithCron("tick", "* * * * * *", func(context.Context) error {
			atomic.AddInt32(&hits, 1)
			return nil
		}),
	)
	if svc.scheduler == nil {
		t.Fatal("scheduler should be non-nil after WithCron")
	}
	// Two ticks worth of wall-clock to absorb the boundary.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&hits) >= 1 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("job never fired within 3s; hits = %d", atomic.LoadInt32(&hits))
}

func TestCron_InvalidSchedule_Errors(t *testing.T) {
	cfg := Config{}
	cfg.Service.LogLevel = "error"
	_, err := New[map[string]any, any](context.Background(), cfg,
		WithCron("bad", "not a cron expr", func(context.Context) error { return nil }))
	if err == nil {
		t.Fatal("expected error from invalid schedule")
	}
}

func TestCron_CustomSlug_Respected(t *testing.T) {
	svc := newServiceForCronTest(t,
		WithCron("daily-rollup", "0 3 * * *", func(context.Context) error { return nil }),
		WithCronSlug("daily-rollup", "orders-daily-rollup"),
	)
	if got := svc.cronSlug("daily-rollup"); got != "orders-daily-rollup" {
		t.Errorf("slug = %q, want orders-daily-rollup", got)
	}
}

func TestCron_DefaultSlug_FromJobName(t *testing.T) {
	svc := newServiceForCronTest(t,
		WithCron("Daily Rollup", "0 3 * * *", func(context.Context) error { return nil }),
	)
	if got := svc.cronSlug("Daily Rollup"); got != "daily-rollup" {
		t.Errorf("slug = %q, want daily-rollup", got)
	}
}
