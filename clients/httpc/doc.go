// Package httpc is the kit's outbound HTTP client builder. It returns a
// stdlib *http.Client whose transport wraps a per-attempt timeout, jittered
// exponential retry on transient failures (5xx, 429, 408, network errors,
// idempotent methods only), and opt-in slog/Prometheus observability.
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
// See docs/superpowers/specs/2026-05-25-kit-httpc-design.md for the full design.
package httpc
