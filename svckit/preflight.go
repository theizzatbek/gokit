package svckit

import (
	"context"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"

	xerrs "github.com/theizzatbek/gokit/errs"
	"github.com/theizzatbek/gokit/fibermap"
)

// PreflightResult is the aggregate outcome of a [Service.Preflight] run.
//
// Status is "ok" when every check passes, "fail" otherwise. Checks
// contains one entry per [fibermap.Checker] the service was wired
// with — see [Service.readinessCheckers] for what contributes to that
// set (DB, mod-added checkers, [WithReadinessChecker] extras). The
// order matches readinessCheckers() so operators reading the JSON
// top-to-bottom see the dependency tree.
type PreflightResult struct {
	Status string           `json:"status"`
	Checks []PreflightCheck `json:"checks"`
}

// PreflightCheck is one check entry. Latency captures the time the
// check took to return; useful for spotting slow dependencies even
// when nothing failed.
type PreflightCheck struct {
	Name    string        `json:"name"`
	Status  string        `json:"status"` // "ok" | "fail"
	Error   string        `json:"error,omitempty"`
	Latency time.Duration `json:"latency_ms"`
}

// Preflight runs every readiness checker the service was wired with
// and returns a structured result. Unlike the K8s readiness probe
// (live state, sub-5s deadlines), Preflight is for "is this
// deployment correctly configured to take traffic" — checks may take
// longer (schema-version SELECT, NATS stream existence verification,
// S3 HEAD probe).
//
// Call from main() right after [New] and before [Run] to fail-fast on
// misconfiguration. Or wire as the `/preflight` HTTP endpoint via
// [WithPreflightEndpoint] for ops smoke-tests and CI gates.
//
// The returned PreflightResult is always populated, success or
// failure, so callers can log/render the full per-check breakdown
// either way. err is nil when every check passed; otherwise it is a
// [*errs.Error] of CodePreflightFailed describing the first failing
// check.
func (s *Service[T, C]) Preflight(ctx context.Context) (PreflightResult, error) {
	res := s.preflightResult(ctx)
	if res.Status != "ok" {
		// First failure — useful for a one-line stderr summary
		// without the caller having to walk PreflightResult.Checks.
		var first string
		for _, c := range res.Checks {
			if c.Status != "ok" {
				first = c.Name + ": " + c.Error
				break
			}
		}
		return res, xerrs.Validationf(CodePreflightFailed, "svckit: preflight failed: %s", first)
	}
	return res, nil
}

// preflightResult runs every checker concurrently under
// preflightTimeout (default 10s) and returns the structured result.
// Each check's latency is recorded individually.
//
// Safe to call repeatedly; checks may have their own internal state
// (connection pool warmup) that benefits from warm runs vs cold.
func (s *Service[T, C]) preflightResult(ctx context.Context) PreflightResult {
	checkers := s.readinessCheckers()
	res := PreflightResult{Status: "ok", Checks: make([]PreflightCheck, len(checkers))}
	if len(checkers) == 0 {
		return res
	}
	timeout := s.opts.preflightTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var wg sync.WaitGroup
	for i, ch := range checkers {
		wg.Add(1)
		go func(i int, ch fibermap.Checker) {
			defer wg.Done()
			start := time.Now()
			err := ch.Check(cctx)
			lat := time.Since(start)
			c := PreflightCheck{Name: ch.Name(), Latency: lat / time.Millisecond}
			if err != nil {
				c.Status = "fail"
				c.Error = err.Error()
			} else {
				c.Status = "ok"
			}
			res.Checks[i] = c
		}(i, ch)
	}
	wg.Wait()
	for _, c := range res.Checks {
		if c.Status != "ok" {
			res.Status = "fail"
			break
		}
	}
	return res
}

// preflightHandler returns a fiber.Handler that renders
// [PreflightResult] as JSON. 200 on success, 503 on any failure — the
// latter matches the readiness-probe convention so a load balancer
// pulls the pod from rotation if this is also wired as a K8s
// readinessProbe.
//
// Mounted by runOptions when [WithPreflightEndpoint] was passed; off
// by default (see [Service.Preflight] for the always-available
// programmatic entry point).
func (s *Service[T, C]) preflightHandler() fiber.Handler {
	return func(c *fiber.Ctx) error {
		res := s.preflightResult(c.UserContext())
		status := fiber.StatusOK
		if res.Status != "ok" {
			status = fiber.StatusServiceUnavailable
		}
		return c.Status(status).JSON(res)
	}
}
