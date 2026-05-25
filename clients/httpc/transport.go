package httpc

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
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

// isRetryableStatus returns true for the hard-coded transient-failure set:
// 408 Request Timeout, 429 Too Many Requests, 500/502/503/504 server errors.
func isRetryableStatus(status int) bool {
	switch status {
	case 408, 429, 500, 502, 503, 504:
		return true
	}
	return false
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var lastResp *http.Response
	for attempt := 0; attempt <= t.maxRetries; attempt++ {
		if err := req.Context().Err(); err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(req.Context(), t.timeout)
		attemptReq := req.Clone(ctx)
		resp, err := t.base.RoundTrip(attemptReq)
		if err != nil {
			cancel()
			// Network errors are NOT retried in Task 6 — only retryable statuses.
			// Task 7 will add network-error retry.
			return nil, err
		}
		if !isRetryableStatus(resp.StatusCode) {
			resp.Body = &cancelOnCloseBody{ReadCloser: resp.Body, cancel: cancel}
			return resp, nil
		}
		// Retryable status: drain + close body, release per-attempt ctx,
		// loop or exit.
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		cancel()
		lastResp = resp
		if attempt >= t.maxRetries {
			// Exhausted: return the last retryable response with a no-op body
			// so callers can still inspect StatusCode/Headers.
			lastResp.Body = io.NopCloser(bytes.NewReader(nil))
			return lastResp, nil
		}
	}
	return lastResp, nil
}
