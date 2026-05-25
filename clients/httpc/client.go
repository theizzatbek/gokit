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
	cols := newCollectors(o.metrics)
	retry := &retryTransport{
		base:        base,
		timeout:     cfg.Timeout,
		maxRetries:  cfg.MaxRetries,
		backoffBase: cfg.BackoffBase,
		backoffMax:  cfg.BackoffMax,
		logger:      o.logger,
		collectors:  cols,
	}
	if cols == nil {
		return retry, nil
	}
	return &metricsTransport{base: retry, collectors: cols}, nil
}
