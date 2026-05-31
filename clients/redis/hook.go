package redisclient

import (
	"context"
	"log/slog"
	"net"
	"time"

	"github.com/redis/go-redis/v9"
)

// hook is the go-redis observability hook. Records every command
// through the metricsCollector + logs at Debug on success / Warn on
// error when a logger is wired.
//
// The hook is installed once at Connect time when WithMetrics is set.
// WithLogger without WithMetrics still installs the hook so logging
// flows without a Prometheus registry — the metricsCollector ref is
// nil-safe.
type hook struct {
	metrics *metricsCollector
	logger  *slog.Logger
}

func newHook(mc *metricsCollector, logger *slog.Logger) redis.Hook {
	return &hook{metrics: mc, logger: logger}
}

// DialHook is pass-through — connection establishment latency is
// already covered by the pool gauge; per-attempt timing isn't
// interesting at the kit observability tier.
func (h *hook) DialHook(next redis.DialHook) redis.DialHook {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		return next(ctx, network, addr)
	}
}

func (h *hook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		start := time.Now()
		err := next(ctx, cmd)
		h.observe(ctx, cmd.Name(), time.Since(start), err)
		return err
	}
}

func (h *hook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		start := time.Now()
		err := next(ctx, cmds)
		// Attribute the timing once per pipeline under the pseudo-
		// command "pipeline"; users wanting per-cmd breakdown should
		// not pipeline. err is the first error in the batch.
		h.observe(ctx, "pipeline", time.Since(start), err)
		return err
	}
}

func (h *hook) observe(ctx context.Context, cmd string, elapsed time.Duration, err error) {
	h.metrics.observe(cmd, elapsed, err)
	if h.logger == nil {
		return
	}
	if err != nil && err != redis.Nil {
		h.logger.WarnContext(ctx, "redis command failed",
			"cmd", cmd, "elapsed", elapsed, "err", err)
		return
	}
	h.logger.DebugContext(ctx, "redis command",
		"cmd", cmd, "elapsed", elapsed)
}
