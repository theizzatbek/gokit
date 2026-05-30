package sentrykit_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"

	"github.com/theizzatbek/gokit/sentrykit"
)

// crontransport implements sentry.Transport so check-in dispatch
// (which goes through Event.CheckIn instead of BeforeSend) is
// observable in tests. Records every SendEvent invocation; never
// emits to the network.
type crontransport struct {
	mu     sync.Mutex
	events []*sentry.Event
}

func (t *crontransport) Configure(_ sentry.ClientOptions)        {}
func (t *crontransport) Flush(_ time.Duration) bool              { return true }
func (t *crontransport) FlushWithContext(_ context.Context) bool { return true }
func (t *crontransport) Close()                                  {}

func (t *crontransport) SendEvent(e *sentry.Event) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.events = append(t.events, e)
}

func (t *crontransport) snapshot() []*sentry.Event {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]*sentry.Event, len(t.events))
	copy(out, t.events)
	return out
}

// installCronClient installs a fresh Sentry client whose transport
// is the supplied captor. Test cleanup restores nothing — the next
// test reinstalls its own client and overrides the global one.
func installCronClient(t *testing.T, tr sentry.Transport) {
	t.Helper()
	if err := sentry.Init(sentry.ClientOptions{
		Dsn:       testDSN,
		Transport: tr,
	}); err != nil {
		t.Fatalf("sentry.Init: %v", err)
	}
	t.Cleanup(func() { sentry.Flush(500 * time.Millisecond) })
}

// checkIns returns the (slug, status) pairs from captured events
// that carry a check-in payload, in order.
type cronTick struct {
	slug   string
	status sentry.CheckInStatus
	dur    time.Duration
	id     sentry.EventID
}

func checkInTicks(events []*sentry.Event) []cronTick {
	var out []cronTick
	for _, e := range events {
		if e.CheckIn == nil {
			continue
		}
		out = append(out, cronTick{
			slug:   e.CheckIn.MonitorSlug,
			status: e.CheckIn.Status,
			dur:    e.CheckIn.Duration,
			id:     e.CheckIn.ID,
		})
	}
	return out
}

func TestMonitorCron_NoSDKRunsFnSilently(t *testing.T) {
	// Reset the global hub to a no-client state.
	sentry.CurrentHub().BindClient(nil)
	called := false
	err := sentrykit.MonitorCron(context.Background(), "no-sdk", func(context.Context) error {
		called = true
		return nil
	})
	if err != nil {
		t.Errorf("MonitorCron no-SDK returned %v, want nil", err)
	}
	if !called {
		t.Error("fn was not invoked when SDK is uninitialised")
	}
}

func TestMonitorCronWithConfig_SendsInProgressThenOK(t *testing.T) {
	tr := &crontransport{}
	installCronClient(t, tr)

	cfg := sentrykit.IntervalMonitorConfig(15 * time.Minute)
	err := sentrykit.MonitorCronWithConfig(context.Background(), "ok-job", cfg, func(context.Context) error {
		return nil
	})
	if err != nil {
		t.Errorf("MonitorCronWithConfig returned %v, want nil", err)
	}
	sentry.Flush(500 * time.Millisecond)

	ticks := checkInTicks(tr.snapshot())
	if len(ticks) != 2 {
		t.Fatalf("ticks = %d, want 2 (InProgress + OK), events=%+v", len(ticks), tr.snapshot())
	}
	if ticks[0].slug != "ok-job" || ticks[0].status != sentry.CheckInStatusInProgress {
		t.Errorf("first tick = %+v, want slug=ok-job status=in_progress", ticks[0])
	}
	if ticks[1].slug != "ok-job" || ticks[1].status != sentry.CheckInStatusOK {
		t.Errorf("second tick = %+v, want slug=ok-job status=ok", ticks[1])
	}
	if ticks[0].id != ticks[1].id || ticks[0].id == "" {
		t.Errorf("check-in IDs must match across InProgress/OK; got %q vs %q", ticks[0].id, ticks[1].id)
	}
}

func TestMonitorCronWithConfig_NonNilErrorSendsErrorStatus(t *testing.T) {
	tr := &crontransport{}
	installCronClient(t, tr)

	boom := errors.New("forced")
	err := sentrykit.MonitorCronWithConfig(context.Background(), "fail-job", nil, func(context.Context) error {
		return boom
	})
	if !errors.Is(err, boom) {
		t.Errorf("MonitorCronWithConfig returned %v, want %v", err, boom)
	}
	sentry.Flush(500 * time.Millisecond)

	ticks := checkInTicks(tr.snapshot())
	if len(ticks) != 2 {
		t.Fatalf("ticks = %d, want 2", len(ticks))
	}
	if ticks[1].status != sentry.CheckInStatusError {
		t.Errorf("final status = %v, want error", ticks[1].status)
	}
}

func TestMonitorCronWithConfig_DurationIsPopulated(t *testing.T) {
	tr := &crontransport{}
	installCronClient(t, tr)

	_ = sentrykit.MonitorCronWithConfig(context.Background(), "slow-job", nil, func(context.Context) error {
		time.Sleep(20 * time.Millisecond)
		return nil
	})
	sentry.Flush(500 * time.Millisecond)

	ticks := checkInTicks(tr.snapshot())
	if len(ticks) != 2 {
		t.Fatalf("ticks = %d, want 2", len(ticks))
	}
	if ticks[1].dur <= 0 {
		t.Errorf("final tick duration = %v, want > 0", ticks[1].dur)
	}
}

func TestIntervalMonitorConfig_DerivesMinutes(t *testing.T) {
	cfg := sentrykit.IntervalMonitorConfig(15 * time.Minute)
	if cfg == nil {
		t.Fatal("nil config")
	}
	if cfg.CheckInMargin != 30 {
		t.Errorf("CheckInMargin = %d, want 30 (2×15 capped at 30)", cfg.CheckInMargin)
	}
	if cfg.MaxRuntime != 30 {
		t.Errorf("MaxRuntime = %d, want 30", cfg.MaxRuntime)
	}
}

func TestIntervalMonitorConfig_SubMinuteClampsToOne(t *testing.T) {
	cfg := sentrykit.IntervalMonitorConfig(30 * time.Second)
	if cfg.CheckInMargin != 2 {
		t.Errorf("CheckInMargin = %d, want 2 (sub-minute clamps schedule to 1, threshold 2×1)", cfg.CheckInMargin)
	}
	if cfg.MaxRuntime != 2 {
		t.Errorf("MaxRuntime = %d, want 2", cfg.MaxRuntime)
	}
}

func TestIntervalMonitorConfig_HourClampsAt30(t *testing.T) {
	cfg := sentrykit.IntervalMonitorConfig(60 * time.Minute)
	if cfg.CheckInMargin != 30 {
		t.Errorf("CheckInMargin = %d, want 30 (cap)", cfg.CheckInMargin)
	}
	if cfg.MaxRuntime != 30 {
		t.Errorf("MaxRuntime = %d, want 30 (cap)", cfg.MaxRuntime)
	}
}
