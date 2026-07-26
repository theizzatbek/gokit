package svckit

import (
	"context"
	"time"
)

// Close releases resources in the reverse order of construction. It is
// idempotent and safe to call on a nil receiver.
//
// There is no tail here enumerating subsystems, and there must not be
// one: every mod closes itself through Host.OnShutdown, and the LIFO
// order gets the right sequencing for free.
func (s *Service[T, C]) Close() {
	if s == nil {
		return
	}
	s.shutdownMu.Lock()
	if s.closed {
		s.shutdownMu.Unlock()
		return
	}
	s.closed = true
	fns := s.shutdownFns
	s.shutdownFns = nil
	s.shutdownMu.Unlock()

	// Cancel the long-lived ctx BEFORE the chain, so ctx-aware workers
	// get a chance to exit before the scheduler-stop deadline.
	if s.runCancel != nil {
		s.runCancel()
	}

	for i := len(fns) - 1; i >= 0; i-- {
		if err := fns[i](); err != nil && s.logger != nil {
			s.logger.Error("svckit: OnShutdown handler failed", "index", i, "err", err)
		}
	}

	if s.DB != nil {
		drainTimeout := s.opts.dbDrainTimeout
		if drainTimeout <= 0 {
			drainTimeout = 5 * time.Second
		}
		ctx, cancel := context.WithTimeout(context.Background(), drainTimeout)
		_ = s.DB.Drain(ctx)
		cancel()
	}
}
