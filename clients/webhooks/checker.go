package webhooks

import (
	"context"

	xerrs "github.com/theizzatbek/gokit/errs"
)

// CodeNotReady is the *errs.Error.Code surfaced by [Checker.Check]
// when the DeliveryStore is unreachable.
const CodeNotReady = "webhooks_not_ready"

// Checker is the readiness adapter that satisfies the structural
// interface expected by `fibermap.Readiness`:
//
//	type Checker interface {
//	    Name() string
//	    Check(ctx context.Context) error
//	}
//
// The adapter pings the underlying DeliveryStore via Claim(ctx, 0) —
// a zero-batch claim should never modify state but does prove the
// store is reachable. Implementations that don't tolerate
// batch_size=0 should wrap with their own probe; the kit's
// `storepg.New` returns an empty slice for batch_size <= 0 by design.
type Checker struct {
	store DeliveryStore
	name  string
}

// NewChecker returns a Checker over the supplied DeliveryStore.
// `name` labels the entry under `checks: {…}` on a degraded /readyz
// response — defaults to "webhooks" when empty.
func NewChecker(store DeliveryStore, name string) *Checker {
	if name == "" {
		name = "webhooks"
	}
	return &Checker{store: store, name: name}
}

// Name implements the readiness Checker interface.
func (c *Checker) Name() string { return c.name }

// Check returns nil iff the underlying DeliveryStore responds to a
// zero-batch Claim. Nil receiver / nil store surfaces as
// `webhooks_not_ready` rather than panicking.
func (c *Checker) Check(ctx context.Context) error {
	if c == nil || c.store == nil {
		return xerrs.Unavailable(CodeNotReady, "webhooks: store not initialised")
	}
	if _, err := c.store.Claim(ctx, 0); err != nil {
		return xerrs.Wrap(err, xerrs.KindUnavailable, CodeNotReady,
			"webhooks: store claim failed")
	}
	return nil
}
