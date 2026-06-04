package migrate

import (
	"context"

	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/db/lock"
)

// Stable Code constants surfaced by the lock-aware Up paths.
const (
	// CodeLockFailed — Acquire on the migration lock errored
	// (conn drop, pool exhausted, server down). Wraps the underlying
	// cause via Cause.
	CodeLockFailed = "migrate_lock_failed"
)

// applyConfig collects the optional knobs that Up / UpTo accept.
type applyConfig struct {
	lockName string // empty = no lock
}

// Option configures Up / UpTo. Reserved for additive features; current
// options are listed below.
type Option func(*applyConfig)

// WithLock wraps the apply loop in an advisory lock named name. Only
// one replica acquires the lock at a time; the others block on
// `pg_advisory_lock`, then drain the (now-applied) set of pending
// migrations as a no-op. The lock auto-releases when the conn is
// returned to the pool, so a crash during apply doesn't strand the
// lock.
//
// Use the same name across every replica that shares one Postgres —
// the kit derives the lock key from sha256(name), so identical names
// always collide. A typical wiring:
//
//	migrate.Up(ctx, svc.DB, migrationsFS,
//	    migrate.WithLock("myservice.migrations"))
//
// Empty name is treated as "no lock" — same as omitting the option.
//
// REQUIREMENT: the underlying *db.DB pool must have MaxConns >= 2.
// WithLock holds a dedicated conn for the advisory lock; the apply
// transactions need separate conns from the same pool. A pool of 1
// will deadlock — the lock owns the only conn, applyOne can't open
// a Tx. Production deployments easily clear this bar; the
// requirement is documented here for the rare 1-conn pool case
// (typically tests).
func WithLock(name string) Option {
	return func(c *applyConfig) {
		if name == "" {
			return
		}
		c.lockName = name
	}
}

// applyWithOptionalLock runs fn under the migration lock when
// cfg.lockName is set, else runs it directly. The shared shim
// keeps Up / UpTo bodies free of lock branching.
func applyWithOptionalLock(ctx context.Context, d *db.DB, cfg applyConfig, fn func(context.Context) error) error {
	if cfg.lockName == "" {
		return fn(ctx)
	}
	lk := lock.New(d, "migrate."+cfg.lockName)
	release, err := lk.Acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	return fn(ctx)
}
