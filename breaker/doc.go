// Package breaker is the kit's generic three-state circuit breaker.
//
// The breaker counts failures within a rolling time window. Once
// the failure threshold is reached AND the minimum-request floor is
// met, the breaker trips to open: every Allow short-circuits with
// ErrOpen for OpenInterval, after which a small number of probe
// calls (HalfOpenMaxProbes) are let through. If all probes succeed
// the breaker closes; the first probe failure re-opens it.
//
// Typical use, via the ergonomic Execute wrapper:
//
//	b, err := breaker.New(breaker.Config{
//	    Name:              "stripe",
//	    FailureThreshold:  20,           // 20 failures
//	    MinimumRequests:   20,           //   within at least 20 calls
//	    WindowDuration:    10 * time.Second,
//	    OpenInterval:      30 * time.Second,
//	    HalfOpenMaxProbes: 1,
//	    Logger:            logger,
//	    Metrics:           reg,
//	})
//	if err != nil { return err }
//
//	if err := b.Execute(func() error { return callStripe(ctx) }); err != nil {
//	    if errors.Is(err, breaker.ErrOpen) {
//	        // short-circuited — Stripe is currently considered down.
//	    }
//	    return err
//	}
//
// Adapters that need to inspect the response before deciding success
// (clients/httpc is the canonical example — an HTTP 200 is success
// even if RoundTrip returned a body-decode err) use the two-phase
// Allow form:
//
//	allowed, done := b.Allow()
//	if !allowed { return breaker.ErrOpen }
//	resp, err := transport.RoundTrip(req)
//	done(success(resp, err))      // caller-defined success classifier
//	return resp, err
//
// (*Breaker)(nil) is a safe no-op receiver: Allow always permits and
// Execute just runs fn. This lets callers thread an optional breaker
// through their code without nil-checking at every call site.
//
// Concurrency: every state read and mutation is guarded by an
// internal sync.Mutex. Execute's fn call is OUTSIDE the lock so
// long-running RPCs do not block other callers' Allow.
package breaker
