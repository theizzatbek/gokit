package redisclient

import (
	"context"

	xerrs "github.com/theizzatbek/gokit/errs"
)

// CodeNotReady — Redis PING failed (closed pool, network error,
// auth denied). Used by Checker.Check.
const CodeNotReady = "redis_not_ready"

// Checker is the readiness adapter that satisfies the structural
// interface expected by `fibermap.Readiness`:
//
//	type Checker interface {
//	    Name() string
//	    Check(ctx context.Context) error
//	}
//
// The adapter does NOT depend on `fibermap` — the interface is
// duck-typed, so consumers can compose checkers from any subpackage
// without coupling.
type Checker struct {
	cl   *Client
	name string
}

// NewChecker returns a Checker over an existing *Client. `name`
// labels the entry under `checks: {…}` on a degraded /readyz
// response — defaults to "redis" when empty.
func NewChecker(cl *Client, name string) *Checker {
	if name == "" {
		name = "redis"
	}
	return &Checker{cl: cl, name: name}
}

// Name implements the readiness Checker interface.
func (c *Checker) Name() string { return c.name }

// Check issues a PING under the caller's context. Nil receiver or
// nil/closed client surfaces as `redis_not_ready` — readiness must
// never crash. PING is the canonical liveness probe for Redis;
// it round-trips the protocol and reflects auth/permission failures.
func (c *Checker) Check(ctx context.Context) error {
	if c == nil || c.cl == nil || c.cl.rdb == nil {
		return xerrs.Unavailable(CodeNotReady, "redis: client not initialised")
	}
	if err := c.cl.rdb.Ping(ctx).Err(); err != nil {
		return xerrs.Wrap(err, xerrs.KindUnavailable, CodeNotReady, "redis: ping failed")
	}
	return nil
}
