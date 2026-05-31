package natsclient

import (
	"context"

	"github.com/nats-io/nats.go"

	"github.com/theizzatbek/gokit/errs"
)

// CodeNotReady — NATS conn is not in CONNECTED state (closed,
// draining, reconnecting). Used by Checker.Check.
const CodeNotReady = "nats_not_ready"

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
// response — defaults to "nats" when empty.
func NewChecker(cl *Client, name string) *Checker {
	if name == "" {
		name = "nats"
	}
	return &Checker{cl: cl, name: name}
}

// Name implements the readiness Checker interface.
func (c *Checker) Name() string { return c.name }

// Check returns nil iff the underlying connection is CONNECTED.
// Nil receiver / nil client / nil conn surface as `nats_not_ready`
// rather than panicking — readiness must never crash the probe
// handler. Uses FlushWithContext to round-trip a PING through the
// server, so a TCP-alive but server-side-broken state is caught.
func (c *Checker) Check(ctx context.Context) error {
	if c == nil || c.cl == nil || c.cl.conn == nil {
		return errs.Unavailable(CodeNotReady, "nats: client not initialised")
	}
	if status := c.cl.conn.Status(); status != nats.CONNECTED {
		return errs.Unavailablef(CodeNotReady, "nats: connection status %s", status)
	}
	if err := c.cl.conn.FlushWithContext(ctx); err != nil {
		return errs.Wrap(err, errs.KindUnavailable, CodeNotReady,
			"nats: server flush failed")
	}
	return nil
}
