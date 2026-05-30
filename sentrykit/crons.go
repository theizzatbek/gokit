package sentrykit

import (
	"context"
	"time"

	"github.com/getsentry/sentry-go"
)

// MonitorCron wraps fn so each invocation sends a Sentry Crons
// check-in: `in_progress` before, `ok` on nil return, `error` on
// non-nil. Use when the monitor is already configured in the Sentry
// UI; the kit doesn't ship a MonitorConfig from code in this
// variant.
//
// When the global Sentry hub is uninitialised (sentrykit.Setup
// hasn't run, or the caller passed an empty DSN), MonitorCron skips
// the check-in dispatch and runs fn directly. Lets schedulers
// thread MonitorCron through unconditionally — the no-op path is
// branch-light (one hub.Client() nil check).
//
// Returns the error from fn unchanged so the caller can still log
// / count it independently of Sentry. Panics in fn propagate; the
// `ok`/`error` check-in is NOT sent in that case (the wider crash
// path will surface the panic via the global hub).
func MonitorCron(ctx context.Context, slug string, fn func(context.Context) error) error {
	return monitorCron(ctx, slug, nil, fn)
}

// MonitorCronWithConfig is MonitorCron plus a MonitorConfig that
// upserts the monitor's expected schedule + thresholds in Sentry on
// each check-in. Use when the schedule is known from code (e.g. an
// interval ticker) so operators don't have to maintain it
// separately in the UI.
//
// Same no-op behaviour as MonitorCron when Sentry is uninitialised.
func MonitorCronWithConfig(ctx context.Context, slug string, cfg *sentry.MonitorConfig, fn func(context.Context) error) error {
	return monitorCron(ctx, slug, cfg, fn)
}

func monitorCron(ctx context.Context, slug string, cfg *sentry.MonitorConfig, fn func(context.Context) error) error {
	hub := sentry.GetHubFromContext(ctx)
	if hub == nil {
		hub = sentry.CurrentHub()
	}
	if hub == nil || hub.Client() == nil {
		return fn(ctx)
	}

	startID := hub.CaptureCheckIn(&sentry.CheckIn{
		MonitorSlug: slug,
		Status:      sentry.CheckInStatusInProgress,
	}, cfg)

	start := time.Now()
	err := fn(ctx)
	status := sentry.CheckInStatusOK
	if err != nil {
		status = sentry.CheckInStatusError
	}

	finish := &sentry.CheckIn{
		MonitorSlug: slug,
		Status:      status,
		Duration:    time.Since(start),
	}
	if startID != nil {
		finish.ID = *startID
	}
	hub.CaptureCheckIn(finish, cfg)
	return err
}

// IntervalMonitorConfig builds a MonitorConfig from a time.Duration
// interval. Schedule unit: minutes (rounded down to a whole minute,
// minimum 1). CheckInMargin and MaxRuntime: 2 × interval minutes,
// each capped at 30 minutes.
//
// Use this for kit-internal periodic jobs whose schedule is known
// at startup. Bespoke crons can construct sentry.MonitorConfig
// directly when they need different thresholds or a crontab string.
func IntervalMonitorConfig(interval time.Duration) *sentry.MonitorConfig {
	minutes := int64(interval / time.Minute)
	if minutes < 1 {
		minutes = 1
	}
	threshold := minutes * 2
	if threshold > 30 {
		threshold = 30
	}
	return &sentry.MonitorConfig{
		Schedule:      sentry.IntervalSchedule(minutes, sentry.MonitorScheduleUnitMinute),
		CheckInMargin: threshold,
		MaxRuntime:    threshold,
	}
}
