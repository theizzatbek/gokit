package outbox

import (
	"context"
	"time"

	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/errs"
)

// CodeBacklog — outbox.Checker.Check reports the pending queue
// has crossed a configured threshold (depth or age).
const CodeBacklog = "outbox_backlog"

// CheckerOption tunes [NewChecker].
type CheckerOption func(*checkerConfig)

type checkerConfig struct {
	maxDepth int
	maxLag   time.Duration
	name     string
}

// WithMaxDepth fails the check when the number of unpublished rows
// exceeds n. Default 10000 — high enough that bursty traffic
// doesn't trip readiness, low enough that runaway backlog surfaces
// before the queue dominates the table. Pass 0 to disable the
// depth check.
func WithMaxDepth(n int) CheckerOption {
	return func(c *checkerConfig) { c.maxDepth = n }
}

// WithMaxLag fails the check when the OLDEST unpublished row is
// older than d. Default 10 minutes — catches a stuck worker before
// downstream consumers notice missing events. Pass 0 to disable
// the lag check.
//
// Lag is computed from `created_at`, not `next_retry_at`, so
// legitimately-backed-off failures count toward the threshold.
// That's intentional — a row scheduled to retry in 30 minutes is
// still a 30-minute-late event from the producer's perspective.
func WithMaxLag(d time.Duration) CheckerOption {
	return func(c *checkerConfig) { c.maxLag = d }
}

// WithCheckerName overrides the name surfaced in /readyz's
// `checks: {...}` body. Default "outbox" — override when running
// multiple outbox tables (e.g. domain-segmented queues) in one
// service.
func WithCheckerName(name string) CheckerOption {
	return func(c *checkerConfig) { c.name = name }
}

// Checker is the [fibermap.Checker] implementation that surfaces
// outbox backlog on the readiness endpoint. service.WithOutbox
// auto-adds one with default thresholds; pass
// [service.WithOutboxReadinessOpts] to tune.
//
// The check is cheap — one SELECT with two aggregates against the
// partial index that already exists for the worker's polling
// path. No new index required.
type Checker struct {
	db   *db.DB
	cfg  checkerConfig
	name string
}

const (
	defaultCheckerMaxDepth = 10000
	defaultCheckerMaxLag   = 10 * time.Minute
	defaultCheckerName     = "outbox"
)

// NewChecker constructs a Checker over an existing *db.DB. Returns
// nil when d is nil so callers can wire
// `outbox.NewChecker(svc.DB, opts...)` unconditionally — the
// readiness handler treats nil checkers as missing subsystems and
// skips them.
func NewChecker(d *db.DB, opts ...CheckerOption) *Checker {
	if d == nil {
		return nil
	}
	cfg := checkerConfig{
		maxDepth: defaultCheckerMaxDepth,
		maxLag:   defaultCheckerMaxLag,
		name:     defaultCheckerName,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	return &Checker{db: d, cfg: cfg, name: cfg.name}
}

// Name implements the readiness Checker interface.
func (c *Checker) Name() string {
	if c == nil || c.name == "" {
		return defaultCheckerName
	}
	return c.name
}

// Check runs `SELECT count(*), MIN(created_at) FROM outbox WHERE
// published_at IS NULL` and compares against the configured
// thresholds.
//
// Nil receiver or nil DB returns nil (treated as "no opinion" —
// the readiness handler still passes if no other check fails).
//
// Lag is computed only when MaxLag > 0 AND at least one row is
// pending — an empty queue can't be late.
func (c *Checker) Check(ctx context.Context) error {
	if c == nil || c.db == nil {
		return nil
	}
	var (
		depth  int
		oldest *time.Time
	)
	row := c.db.QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE published_at IS NULL),
			MIN(created_at) FILTER (WHERE published_at IS NULL)
		FROM outbox
	`)
	if err := row.Scan(&depth, &oldest); err != nil {
		return errs.Wrap(err, errs.KindUnavailable, CodeBacklog,
			"outbox: backlog query failed")
	}
	if c.cfg.maxDepth > 0 && depth > c.cfg.maxDepth {
		return errs.Unavailablef(CodeBacklog,
			"outbox: pending depth %d > max %d", depth, c.cfg.maxDepth)
	}
	if c.cfg.maxLag > 0 && oldest != nil {
		if lag := time.Since(*oldest); lag > c.cfg.maxLag {
			return errs.Unavailablef(CodeBacklog,
				"outbox: oldest pending %s > max %s", lag.Round(time.Second), c.cfg.maxLag)
		}
	}
	return nil
}
