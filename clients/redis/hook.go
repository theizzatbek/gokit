package redisclient

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/theizzatbek/gokit/breaker"
	xerrs "github.com/theizzatbek/gokit/errs"
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
	metrics        *metricsCollector
	logger         *slog.Logger
	defaultTimeout time.Duration
	breaker        *breaker.Breaker
}

func newHook(mc *metricsCollector, logger *slog.Logger, defaultTimeout time.Duration, br *breaker.Breaker) redis.Hook {
	return &hook{
		metrics:        mc,
		logger:         logger,
		defaultTimeout: defaultTimeout,
		breaker:        br,
	}
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
		ctx, cancel := h.deriveTimeoutCtx(ctx)
		if cancel != nil {
			defer cancel()
		}
		start := time.Now()
		err := h.executeWithBreaker(func() error { return next(ctx, cmd) })
		h.observe(ctx, cmd.Name(), time.Since(start), err)
		return err
	}
}

func (h *hook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		ctx, cancel := h.deriveTimeoutCtx(ctx)
		if cancel != nil {
			defer cancel()
		}
		start := time.Now()
		err := h.executeWithBreaker(func() error { return next(ctx, cmds) })
		// Attribute the timing once per pipeline under the pseudo-
		// command "pipeline"; users wanting per-cmd breakdown should
		// not pipeline. err is the first error in the batch.
		h.observe(ctx, "pipeline", time.Since(start), err)
		return err
	}
}

// deriveTimeoutCtx applies WithDefaultTimeout iff the caller's ctx
// has no deadline already (explicit deadlines always win).
func (h *hook) deriveTimeoutCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	if h.defaultTimeout <= 0 {
		return ctx, nil
	}
	if _, has := ctx.Deadline(); has {
		return ctx, nil
	}
	return context.WithTimeout(ctx, h.defaultTimeout)
}

// executeWithBreaker routes the next-call through the configured
// *breaker.Breaker. nil breaker = direct invocation. redis.Nil is
// preserved through breaker.Execute as a success outcome — the
// breaker classifier treats nil as success and any other err
// (including redis.Nil, which we WANT to count as success operationally)
// as a failure. To keep the operational meaning, we filter redis.Nil
// to "no err" inside the wrapper so the breaker doesn't trip on the
// "key not found" path; the original err still surfaces to the
// caller.
func (h *hook) executeWithBreaker(fn func() error) error {
	if h.breaker == nil {
		return fn()
	}
	var realErr error
	bErr := h.breaker.Execute(func() error {
		realErr = fn()
		if errors.Is(realErr, redis.Nil) {
			return nil
		}
		return realErr
	})
	if errors.Is(bErr, breaker.ErrOpen) {
		return xerrs.Wrap(breaker.ErrOpen, xerrs.KindUnavailable,
			CodeCircuitOpen, "redisclient: circuit open")
	}
	return realErr
}

func (h *hook) observe(ctx context.Context, cmd string, elapsed time.Duration, err error) {
	h.metrics.observe(cmd, elapsed, err)
	if h.logger == nil {
		return
	}
	if err != nil && !errors.Is(err, redis.Nil) {
		h.logger.WarnContext(ctx, "redis command failed",
			"cmd", cmd, "elapsed", elapsed, "err", err)
		return
	}
	h.logger.DebugContext(ctx, "redis command",
		"cmd", cmd, "elapsed", elapsed)
}
