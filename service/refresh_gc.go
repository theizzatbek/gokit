package service

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/theizzatbek/gokit/sentrykit"
)

// startRefreshGC spawns the goroutine that periodically calls
// Store.GarbageCollect. Returns immediately when the option is disabled
// or the prerequisites (Auth + refreshStore) aren't wired. The
// goroutine is bound to a fresh context.WithCancel; the cancel is
// registered via OnShutdown so Close() stops the ticker before tearing
// down DB. Returns (stop func()) so tests can stop the goroutine
// without going through Close.
func (s *Service[T, C]) startRefreshGC() {
	if s.opts == nil || s.opts.refreshGCInterval <= 0 {
		return
	}
	if s.Auth == nil || s.refreshStore == nil {
		return
	}
	interval := s.opts.refreshGCInterval
	store := s.refreshStore
	logger := s.logger

	// Cron monitor wiring: when Sentry is also configured AND the
	// caller didn't opt out, each tick reports a Sentry Crons
	// check-in. When Sentry is off, monitorTick is a transparent
	// pass-through (sentrykit.MonitorCronWithConfig no-ops when the
	// hub has no client).
	useMonitor := s.sentryShutdown != nil && !s.opts.skipSentryRefreshGCMonitor
	slug := s.opts.sentryRefreshGCSlug
	if slug == "" {
		slug = "kit-refresh-gc"
	}
	monitorCfg := sentrykit.IntervalMonitorConfig(interval)

	ctx, cancel := context.WithCancel(context.Background())
	var done sync.WaitGroup
	done.Add(1)
	go func() {
		defer done.Done()
		defer func() {
			if r := recover(); r != nil && logger != nil {
				logger.Error("service: refresh GC goroutine panicked", "panic", r)
			}
		}()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				runCtx, runCancel := context.WithTimeout(ctx, interval)
				tick := func(ctx context.Context) error {
					n, err := store.GarbageCollect(ctx, time.Now())
					if err != nil {
						return err
					}
					if logger != nil && n > 0 {
						logger.Info("service: refresh GC", "removed", n)
					}
					return nil
				}
				var err error
				if useMonitor {
					err = sentrykit.MonitorCronWithConfig(runCtx, slug, monitorCfg, tick)
				} else {
					err = tick(runCtx)
				}
				runCancel()
				if err != nil {
					if errors.Is(err, context.Canceled) {
						return
					}
					if logger != nil {
						logger.Warn("service: refresh GC failed", "err", err)
					}
					continue
				}
			}
		}
	}()

	s.OnShutdown(func() error {
		cancel()
		done.Wait()
		return nil
	})
}
