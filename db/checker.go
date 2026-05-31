package db

import (
	"context"

	"github.com/theizzatbek/gokit/errs"
)

// Checker is the readiness adapter that satisfies the structural
// interface expected by `fibermap.Readiness`:
//
//	type Checker interface {
//	    Name() string
//	    Check(ctx context.Context) error
//	}
//
// Construct via [NewChecker]; pass into `fibermap.WithReadiness`
// or `service.New` (which auto-wires DB checks at /readyz).
//
// The adapter intentionally does NOT depend on `fibermap` — the
// interface is duck-typed, so consumers can compose checkers from
// any subpackage without coupling.
type Checker struct {
	db   *DB
	name string
}

// NewChecker returns a Checker that delegates to [DB.Healthcheck].
// `name` is the label surfaced in the /readyz body under
// `checks: {…}` — defaults to "db" when empty.
func NewChecker(d *DB, name string) *Checker {
	if name == "" {
		name = "db"
	}
	return &Checker{db: d, name: name}
}

// Name implements the readiness Checker interface.
func (c *Checker) Name() string { return c.name }

// Check runs Healthcheck under the caller's context. Nil receiver
// or nil DB pool surfaces as `db_unavailable` so a misconfigured
// readiness wiring fails loudly instead of pretending healthy.
func (c *Checker) Check(ctx context.Context) error {
	if c == nil || c.db == nil {
		return errs.Unavailable("db_unavailable", "db: checker has no pool")
	}
	return c.db.Healthcheck(ctx)
}
