package service

import (
	"errors"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"

	"github.com/theizzatbek/gokit/fibermap"
)

// registerRuntimeCollectors registers the Go runtime + process
// collectors on s.metrics so the auto-mounted /metrics endpoint
// surfaces `go_*` (goroutines, heap, GC) and `process_*` (FDs, RSS,
// CPU seconds) series alongside the kit's subsystem metrics.
//
// Idempotent against prometheus.AlreadyRegisteredError — if the user
// pre-registered the same collectors on their own registry passed
// via [WithMetrics], the duplicate registration is silently swallowed.
func (s *Service[T, C]) registerRuntimeCollectors() {
	if s.opts.skipRuntimeMetrics || s.metrics == nil {
		return
	}
	for _, c := range []prometheus.Collector{
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	} {
		if err := s.metrics.Register(c); err != nil {
			var dup prometheus.AlreadyRegisteredError
			if !errors.As(err, &dup) {
				// Should not happen for stock collectors with no constructor opts.
				// Log instead of panic — metrics are best-effort.
				if s.logger != nil {
					s.logger.Warn("service: failed to register runtime collector", "err", err)
				}
			}
		}
	}
}

// metricsRegistry returns s.metrics as a fibermap.MetricsRegistry
// (both Registerer + Gatherer) so the /metrics endpoint can serve
// it. Returns nil when s.metrics is a Registerer-only type — the
// caller falls back to fibermap's private registry.
func (s *Service[T, C]) metricsRegistry() fibermap.MetricsRegistry {
	if s.metrics == nil {
		return nil
	}
	reg, ok := s.metrics.(fibermap.MetricsRegistry)
	if !ok {
		return nil
	}
	return reg
}