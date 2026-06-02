package httpc

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"math"
	mathrand "math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/theizzatbek/gokit/breaker"
	"github.com/theizzatbek/gokit/bulkhead"
)

// retryTransport is the kit's retry-on-transient-failure RoundTripper.
// Behaviour is documented in docs/superpowers/specs/2026-05-25-kit-httpc-design.md.
type retryTransport struct {
	base        http.RoundTripper
	timeout     time.Duration
	maxRetries  int
	backoffBase time.Duration
	backoffMax  time.Duration
	logger      *slog.Logger
	collectors  *collectors
}

// cancelOnCloseBody chains the per-attempt context's cancel() to the response
// body's Close. This lets us use context.WithTimeout for the per-attempt
// deadline without killing streaming reads after RoundTrip returns.
type cancelOnCloseBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelOnCloseBody) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}

// computeBackoff returns the delay before the next retry attempt, given the
// index of the failed attempt. Full-jitter exponential capped at backoffMax.
//
// Uses the top-level math/rand/v2 functions (thread-safe via per-CPU state)
// rather than a package-level *Rand, which would race when multiple in-flight
// requests retry concurrently through the same transport.
func computeBackoff(attempt int, base, maxDelay time.Duration) time.Duration {
	if base <= 0 {
		return 0
	}
	shift := attempt
	if shift > 30 {
		shift = 30 // clamp to avoid 2^attempt overflow
	}
	exp := float64(uint64(1) << uint(shift))
	capped := time.Duration(math.Min(float64(base)*exp, float64(maxDelay)))
	return time.Duration(mathrand.Float64() * float64(capped))
}

// ctxSleep sleeps for d, returning early with ctx.Err() if the context is
// cancelled. Uses a timer so resources are freed deterministically.
func ctxSleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// idempotentMethods is the hard-coded set of HTTP methods we retry.
// POST and PATCH are excluded to avoid silent double-writes.
var idempotentMethods = map[string]struct{}{
	"GET":     {},
	"HEAD":    {},
	"PUT":     {},
	"DELETE":  {},
	"OPTIONS": {},
}

func isIdempotent(method string) bool {
	_, ok := idempotentMethods[strings.ToUpper(method)]
	return ok
}

// isRetryableStatus returns true for the hard-coded transient-failure set:
// 408 Request Timeout, 429 Too Many Requests, 500/502/503/504 server errors.
func isRetryableStatus(status int) bool {
	switch status {
	case 408, 429, 500, 502, 503, 504:
		return true
	}
	return false
}

// retryAfter returns the duration the response advises waiting, or 0 if no
// usable header is present. Cap is applied by the caller.
func retryAfter(resp *http.Response) time.Duration {
	if resp == nil {
		return 0
	}
	h := resp.Header.Get("Retry-After")
	if h == "" {
		return 0
	}
	// Integer seconds form (RFC 7231 §7.1.3).
	if secs, err := strconv.Atoi(h); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	// HTTP-date form.
	if when, err := http.ParseTime(h); err == nil {
		d := time.Until(when)
		if d < 0 {
			return 0
		}
		return d
	}
	return 0
}

// canReplay reports whether the request body can be rewound for a retry.
// Bodies set via http.NewRequest with strings.Reader, bytes.Buffer, or
// bytes.Reader have GetBody set automatically; streaming bodies (manually
// constructed Request with Body but no GetBody) cannot be replayed.
func canReplay(req *http.Request) bool {
	return req.Body == nil || req.GetBody != nil
}

// rewindBody invokes req.GetBody to produce a fresh Body for the next
// attempt. Caller must ensure canReplay(req) returned true.
func rewindBody(req *http.Request) error {
	if req.Body == nil || req.GetBody == nil {
		return nil
	}
	body, err := req.GetBody()
	if err != nil {
		return err
	}
	req.Body = body
	return nil
}

// classify maps a retry decision to the metric/log classification label.
func classify(resp *http.Response, err error, ra time.Duration) string {
	switch {
	case ra > 0:
		return "retry_after"
	case err != nil || resp == nil:
		return "network"
	case resp.StatusCode == 408:
		return "408"
	case resp.StatusCode == 429:
		return "429"
	case resp.StatusCode >= 500:
		return "5xx"
	}
	return "other"
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	maxAttempts := t.maxRetries
	if !isIdempotent(req.Method) {
		maxAttempts = 0
	}
	for attempt := 0; attempt <= maxAttempts; attempt++ {
		if err := req.Context().Err(); err != nil {
			return nil, err
		}
		// Body replay: on retries (attempt > 0) with a non-nil request body,
		// call GetBody to produce a fresh reader. rewindBody is a no-op when
		// Body is nil; canReplay guards guarantee GetBody is non-nil here.
		if attempt > 0 {
			if err := rewindBody(req); err != nil {
				return nil, err
			}
		}
		ctx, cancel := context.WithTimeout(req.Context(), t.timeout)
		attemptReq := req.Clone(ctx)
		resp, err := t.base.RoundTrip(attemptReq)
		if err != nil {
			cancel()
			// Circuit-open from the breakerTransport below us: do not
			// retry, do not back off — the breaker has already
			// classified the upstream as down and any retry would
			// just burn the per-request budget pointlessly.
			if errors.Is(err, breaker.ErrOpen) {
				return nil, err
			}
			// Bulkhead-full from the bulkheadTransport: the queue is
			// already saturated; piling more retries on it makes it
			// worse. Bail immediately. ErrQueueTimeout is NOT
			// shortcut here — it is treated as a normal transient
			// (the next attempt may succeed once the slot frees).
			if errors.Is(err, bulkhead.ErrBulkheadFull) {
				return nil, err
			}
			if attempt >= maxAttempts {
				if t.logger != nil {
					t.logger.WarnContext(req.Context(), "httpc retries exhausted",
						"method", req.Method, "url", req.URL.String(),
						"attempts", attempt+1, "err", err.Error())
				}
				if t.collectors != nil {
					t.collectors.retriesExhausted.WithLabelValues(req.Method).Inc()
				}
				return nil, err
			}
			// If the body cannot be replayed, stop retrying and return the error.
			if !canReplay(req) {
				return nil, err
			}
			delay := computeBackoff(attempt, t.backoffBase, t.backoffMax)
			if t.logger != nil {
				t.logger.DebugContext(req.Context(), "httpc retry",
					"method", req.Method, "url", req.URL.String(),
					"attempt", attempt, "delay_ms", delay.Milliseconds(),
					"err", err.Error())
			}
			if t.collectors != nil {
				t.collectors.retriesTotal.WithLabelValues(req.Method, "network").Inc()
			}
			if sleepErr := ctxSleep(req.Context(), delay); sleepErr != nil {
				return nil, sleepErr
			}
			continue
		}
		if !isRetryableStatus(resp.StatusCode) {
			resp.Body = &cancelOnCloseBody{ReadCloser: resp.Body, cancel: cancel}
			return resp, nil
		}
		// Retryable status: drain + close body, release per-attempt ctx.
		ra := retryAfter(resp)
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		cancel()
		if attempt == maxAttempts {
			// Exhausted: return drained response so callers can still inspect
			// StatusCode/Headers.
			if t.logger != nil {
				t.logger.WarnContext(req.Context(), "httpc retries exhausted",
					"method", req.Method, "url", req.URL.String(),
					"attempts", attempt+1, "status", resp.StatusCode)
			}
			if t.collectors != nil {
				t.collectors.retriesExhausted.WithLabelValues(req.Method).Inc()
			}
			resp.Body = io.NopCloser(bytes.NewReader(nil))
			return resp, nil
		}
		// If the body cannot be replayed, stop retrying and return what we have.
		if !canReplay(req) {
			resp.Body = io.NopCloser(bytes.NewReader(nil))
			return resp, nil
		}
		var delay time.Duration
		if ra > 0 {
			maxDelay := 4 * t.backoffMax
			if ra > maxDelay {
				delay = maxDelay
			} else {
				delay = ra
			}
		} else {
			delay = computeBackoff(attempt, t.backoffBase, t.backoffMax)
		}
		class := classify(resp, nil, ra)
		if t.logger != nil {
			t.logger.DebugContext(req.Context(), "httpc retry",
				"method", req.Method, "url", req.URL.String(),
				"attempt", attempt, "delay_ms", delay.Milliseconds(),
				"status", resp.StatusCode, "reason", class)
		}
		if t.collectors != nil {
			t.collectors.retriesTotal.WithLabelValues(req.Method, class).Inc()
		}
		if sleepErr := ctxSleep(req.Context(), delay); sleepErr != nil {
			return nil, sleepErr
		}
	}
	// Unreachable: every loop iteration returns.
	panic("httpc: unreachable")
}
