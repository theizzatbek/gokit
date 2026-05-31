package fibermap

import (
	"context"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
)

// Checker is the contract for one readiness probe. Each kit
// subsystem ships an adapter (`db.NewChecker`, `redisclient.NewChecker`,
// `natsclient.NewChecker`) so the typical Service wires the full
// dependency set without bespoke code.
//
// Implementations should be cheap (single ping) and respect the
// supplied context's deadline — the Readiness handler runs them in
// parallel under one shared deadline.
type Checker interface {
	// Name labels the check in the JSON response and operator logs.
	// Convention: short lowercase identifier (e.g. "db", "nats",
	// "redis", "upstream").
	Name() string
	// Check returns nil when the dependency is healthy. Any
	// non-nil error is rendered in the JSON body so K8s log
	// scrapers + operators can see WHY the probe failed.
	Check(ctx context.Context) error
}

const defaultReadinessTimeout = 5 * time.Second

// ReadinessOption configures [Readiness].
type ReadinessOption func(*readinessConfig)

type readinessConfig struct {
	timeout time.Duration
}

// WithReadinessTimeout caps how long Readiness waits on the slowest
// checker. Default 5s. Lower in environments where K8s readiness
// probes are tight; raise when a check is legitimately slow
// (cross-region SaaS ping, etc.).
func WithReadinessTimeout(d time.Duration) ReadinessOption {
	return func(c *readinessConfig) { c.timeout = d }
}

// Readiness returns a fiber.Handler that runs every checker
// concurrently under one shared deadline (default 5s) and
// translates the outcome into:
//
//   - 200 OK with body `{"status":"ok"}` if every check passed.
//   - 503 Service Unavailable with body
//     `{"status":"degraded","checks":{"<name>":"<error>"}}` if any
//     check failed (or its context deadline expired). The map only
//     carries failed checks — successful ones are implied.
//
// The handler is route-handler shape (no c.Next call) so it
// bypasses the Use chain — same convention as the /healthz built-in.
// Wire it into Run via [WithReadiness]. Zero-checker calls return
// the "all-ok" body so a Service that hasn't wired any subsystem
// still passes the probe.
func Readiness(checkers []Checker, opts ...ReadinessOption) fiber.Handler {
	cfg := &readinessConfig{timeout: defaultReadinessTimeout}
	for _, opt := range opts {
		opt(cfg)
	}
	return func(c *fiber.Ctx) error {
		ctx, cancel := context.WithTimeout(c.UserContext(), cfg.timeout)
		defer cancel()

		var (
			mu     sync.Mutex
			failed = map[string]string{}
		)
		var wg sync.WaitGroup
		wg.Add(len(checkers))
		for _, ch := range checkers {
			go func(ch Checker) {
				defer wg.Done()
				if err := ch.Check(ctx); err != nil {
					mu.Lock()
					failed[ch.Name()] = err.Error()
					mu.Unlock()
				}
			}(ch)
		}
		wg.Wait()

		if len(failed) == 0 {
			return c.Status(fiber.StatusOK).JSON(fiber.Map{
				"status": "ok",
			})
		}
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"status": "degraded",
			"checks": failed,
		})
	}
}
