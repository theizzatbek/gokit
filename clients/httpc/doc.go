// Package httpc is the kit's outbound HTTP client builder. New(cfg, opts...)
// returns a stdlib *http.Client whose Transport applies, in order:
//
//   - End-to-end Prometheus instrumentation (when WithMetrics is set).
//   - Retry with full-jitter exponential backoff on transient failures
//     (5xx, 429, 408, network errors), idempotent methods only.
//   - Per-attempt timeout via context.WithTimeout.
//   - The user-supplied or stdlib base transport (override via WithBaseTransport).
//
// Errors returned from New / NewTransport for invalid config are *errs.Error
// (Codes: httpc_invalid_timeout, httpc_invalid_max_retries, httpc_invalid_backoff).
// Network errors and exhausted-retry final results pass through as stdlib
// errors and *http.Response respectively — the client is drop-in compatible
// with anything that accepts a *http.Client.
//
// Typical wiring:
//
//	c, err := httpc.New(httpc.Config{
//	    Timeout:     10 * time.Second,
//	    MaxRetries:  3,
//	    BackoffBase: 100 * time.Millisecond,
//	    BackoffMax:  5 * time.Second,
//	}, httpc.WithLogger(logger), httpc.WithMetrics(reg))
//	if err != nil { return err }
//
//	resp, err := c.Get("https://api.example.com/users/42")
//
// For projects building their own transport stack (otel, custom auth
// middleware), NewTransport returns the same chain unwrapped:
//
//	rt, _ := httpc.NewTransport(cfg)
//	myClient := &http.Client{Transport: otelTransport(rt)}
//
// See docs/superpowers/specs/2026-05-25-kit-httpc-design.md for the full design.
package httpc
