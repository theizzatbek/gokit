package service

import (
	"context"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/theizzatbek/gokit/fibermap"
)

// Stable error Code constants used by Preflight.
const (
	// CodePreflightFailed — at least one preflight check failed.
	// Returned by Preflight when called as a function; the HTTP
	// handler renders the same error via a 503 response.
	CodePreflightFailed = "service_preflight_failed"
)

// PreflightResult is the aggregate outcome of a Preflight run.
//
// Status is "ok" when every check passes, "fail" otherwise. Checks
// contains one entry per [fibermap.Checker] the service was wired
// with — DB / Redis / NATS / Outbox / any [WithReadinessChecker]
// extras. The order matches construction order so operators reading
// the JSON top-to-bottom see the dependency tree.
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
// Call from main() right after [New] and before [Run] to fail-fast
// on misconfiguration. Or wire as the `/preflight` HTTP endpoint via
// [WithPreflightEndpoint] for ops smoke-tests and CI gates.
//
// Returns nil on full success. On any failure, returns a
// [*errs.Error] of CodePreflightFailed; the per-check details live
// in [PreflightResult] (call [Service.PreflightResult] for that).
func (s *Service[T, C]) Preflight(ctx context.Context) error {
	res := s.PreflightResult(ctx)
	if res.Status != "ok" {
		// Aggregate error message — useful for `kit doctor`'s
		// stderr output without parsing JSON.
		var first string
		for _, c := range res.Checks {
			if c.Status != "ok" {
				first = c.Name + ": " + c.Error
				break
			}
		}
		return preflightErrorOf(first)
	}
	return nil
}

// PreflightResult runs every checker concurrently under preflightTimeout
// (default 10s) and returns the structured result. Each check's
// latency is recorded individually.
//
// Safe to call repeatedly; checks may have their own internal state
// (connection pool warmup) that benefits from warm runs vs cold.
func (s *Service[T, C]) PreflightResult(ctx context.Context) PreflightResult {
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
// [PreflightResult] as JSON. 200 on success, 503 on any failure —
// the latter matches the readiness-probe convention so load balancers
// will pull the pod from rotation if `/preflight` is also wired as
// a K8s readinessProbe.
func (s *Service[T, C]) preflightHandler() fiber.Handler {
	return func(c *fiber.Ctx) error {
		res := s.PreflightResult(c.UserContext())
		status := fiber.StatusOK
		if res.Status != "ok" {
			status = fiber.StatusServiceUnavailable
		}
		return c.Status(status).JSON(res)
	}
}

// preflightErrorOf is a tiny helper to keep the import surface tight —
// callers without our errs package see a plain error.
func preflightErrorOf(reason string) error {
	return &preflightErr{reason: reason}
}

type preflightErr struct{ reason string }

func (e *preflightErr) Error() string { return "service: preflight failed: " + e.reason }
