// Package bulkhead is the kit's generic concurrency-cap with bounded
// wait queue. It limits how many simultaneous calls may be in flight
// against a single dependency (typically an upstream HTTP API), so
// one slow apstream cannot wedge the entire worker pool.
//
// Two knobs do the job:
//
//   - MaxConcurrent — hard cap on slots; the (MaxConcurrent+1)-th
//     caller waits.
//   - MaxQueue — cap on waiters; the (MaxConcurrent+MaxQueue+1)-th
//     caller gets ErrBulkheadFull immediately.
//
// Typical use, via the ergonomic Execute wrapper:
//
//	b, err := bulkhead.New(bulkhead.Config{
//	    Name:          "stripe",
//	    MaxConcurrent: 20,
//	    MaxQueue:      50,
//	    QueueTimeout:  100 * time.Millisecond,
//	    Metrics:       reg,
//	})
//	if err != nil { return err }
//
//	if err := b.Execute(ctx, func() error { return callStripe(ctx) }); err != nil {
//	    if errors.Is(err, bulkhead.ErrBulkheadFull) {
//	        // 503-ish fast-fail: Stripe is currently saturated, try a
//	        // fallback or surface "try again later" upstream.
//	    }
//	    return err
//	}
//
// Adapters that need to inspect the response before deciding success
// (clients/httpc is the canonical example) use the two-phase Acquire
// form:
//
//	release, err := b.Acquire(ctx)
//	if err != nil { return err }
//	defer release()
//	resp, err := transport.RoundTrip(req)
//	return resp, err
//
// (*Bulkhead)(nil) is a safe no-op receiver: Acquire always permits
// and Execute just runs fn. This lets callers thread an optional
// bulkhead through their code without nil-checking at every call site.
//
// Concurrency: the in-flight semaphore is a buffered chan of
// struct{}; the waiter counter is an atomic. There are no locks on
// the hot path. Fairness is best-effort (Go's select randomizes
// between ready cases) — bulkhead does not promise FIFO. Callers
// needing strict fairness layer their own queue above.
package bulkhead
