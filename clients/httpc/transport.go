package httpc

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"math"
	mathrand "math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"
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

// rng is the package-level RNG used for backoff jitter. Tests verify
// behaviour (counts, ordering), not exact delays, so a fixed seed is fine.
var rng = mathrand.New(mathrand.NewPCG(1, 2))

// computeBackoff returns the delay before the next retry attempt, given the
// index of the failed attempt. Full-jitter exponential capped at backoffMax.
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
	return time.Duration(rng.Float64() * float64(capped))
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
			if attempt >= maxAttempts {
				return nil, err
			}
			// If the body cannot be replayed, stop retrying and return the error.
			if !canReplay(req) {
				return nil, err
			}
			delay := computeBackoff(attempt, t.backoffBase, t.backoffMax)
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
		if sleepErr := ctxSleep(req.Context(), delay); sleepErr != nil {
			return nil, sleepErr
		}
	}
	// Unreachable: every loop iteration returns.
	panic("httpc: unreachable")
}
