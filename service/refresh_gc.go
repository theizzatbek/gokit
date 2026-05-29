package service

import (
	"context"
	"errors"
	"sync"
	"time"
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
				n, err := store.GarbageCollect(runCtx, time.Now())
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
				if logger != nil && n > 0 {
					logger.Info("service: refresh GC", "removed", n)
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
