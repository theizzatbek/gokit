package httpc

import "net/http"

// New builds a *http.Client whose transport applies the retry/timeout chain
// configured by cfg + opts. The returned client's own Timeout is 0; the
// per-attempt timeout lives inside the retry transport.
func New(cfg Config, opts ...Option) (*http.Client, error) {
	tr, err := NewTransport(cfg, opts...)
	if err != nil {
		return nil, err
	}
	return &http.Client{Transport: tr}, nil
}

// NewTransport returns the retry + per-attempt-timeout RoundTripper as a
// composable layer, for callers building their own *http.Client (e.g. with
// otel instrumentation wrapped around the outside).
func NewTransport(cfg Config, opts ...Option) (http.RoundTripper, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	cfg.applyDefaults()

	o := &options{}
	for _, fn := range opts {
		fn(o)
	}
	base := o.baseTransport
	if base == nil {
		base = http.DefaultTransport
	}
	// Circuit breaker sits BELOW retry so each attempt is consulted
	// independently; when the breaker trips mid-retry, retry sees
	// ErrOpen on the next attempt and bails immediately.
	if o.breaker != nil {
		base = &breakerTransport{
			base:      base,
			breaker:   o.breaker,
			failureFn: o.breakerFailureFn,
		}
	}
	// Bulkhead sits ABOVE the breaker so an open breaker does not
	// occupy a slot — the breakerTransport returns ErrOpen before
	// reaching base, and the deferred release here fires on a slot
	// that was never actually used. Bulkhead is still BELOW retry so
	// each retry attempt Acquires + releases independently (a retry
	// backoff sleep does not camp on a slot).
	if o.bulkhead != nil {
		base = &bulkheadTransport{
			base:     base,
			bulkhead: o.bulkhead,
		}
	}
	cols := newCollectors(o.metrics)
	retry := &retryTransport{
		base:               base,
		timeout:            cfg.Timeout,
		maxRetries:         cfg.MaxRetries,
		backoffBase:        cfg.BackoffBase,
		backoffMax:         cfg.BackoffMax,
		logger:             o.logger,
		collectors:         cols,
		classifier:         o.retryClassifier,
		statusCodes:        o.retryStatusCodes,
		retryNonIdempotent: o.retryNonIdempotent,
		idempotencyKeyHdr:  o.idempotencyKeyHdr,
	}
	var top http.RoundTripper = retry
	if cols != nil {
		top = &metricsTransport{base: retry, collectors: cols}
	}
	// User middleware chain — applied in REVERSE so the FIRST entry
	// in WithMiddleware sees the request FIRST (outermost wrapper).
	for i := len(o.middleware) - 1; i >= 0; i-- {
		if mw := o.middleware[i]; mw != nil {
			top = mw(top)
		}
	}
	if o.beforeRequest != nil || o.afterResponse != nil {
		top = &hookTransport{base: top, before: o.beforeRequest, after: o.afterResponse}
	}
	if !o.skipRequestIDHeader {
		top = &requestIDTransport{base: top}
	}
	return top, nil
}
