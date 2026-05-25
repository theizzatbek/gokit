package httpc

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// collectors holds the package's Prometheus metrics. nil when WithMetrics
// was not used — every increment site nil-checks.
type collectors struct {
	requestsTotal    *prometheus.CounterVec
	requestDuration  *prometheus.HistogramVec
	retriesTotal     *prometheus.CounterVec
	retriesExhausted *prometheus.CounterVec
}

// newCollectors builds and registers the package's Prometheus metrics.
// Returns nil if reg is nil (zero memory cost when metrics are off).
func newCollectors(reg prometheus.Registerer) *collectors {
	if reg == nil {
		return nil
	}
	c := &collectors{
		requestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "httpc_requests_total",
			Help: "Outbound HTTP requests completed, labelled by method and status (status=\"error\" for network failures).",
		}, []string{"method", "status"}),
		requestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "httpc_request_duration_seconds",
			Help:    "End-to-end outbound HTTP request duration including retries.",
			Buckets: prometheus.DefBuckets,
		}, []string{"method", "status"}),
		retriesTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "httpc_retries_total",
			Help: "Retry attempts performed, classified by failure type.",
		}, []string{"method", "classification"}),
		retriesExhausted: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "httpc_retries_exhausted_total",
			Help: "Retry budgets exhausted; the request returned the last attempt's result.",
		}, []string{"method"}),
	}
	reg.MustRegister(c.requestsTotal, c.requestDuration, c.retriesTotal, c.retriesExhausted)
	// Initialise at least one label-set per metric so Gather returns the
	// metric families even before any request is made.
	c.requestsTotal.WithLabelValues("GET", "200")
	c.requestDuration.WithLabelValues("GET", "200")
	c.retriesTotal.WithLabelValues("GET", "network")
	c.retriesExhausted.WithLabelValues("GET")
	return c
}

// metricsTransport observes the end-to-end request (method, status, duration).
// It does NOT count retries; that is the retryTransport's responsibility.
type metricsTransport struct {
	base       http.RoundTripper
	collectors *collectors
}

func (t *metricsTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()
	resp, err := t.base.RoundTrip(req)
	if t.collectors == nil {
		return resp, err
	}
	method := req.Method
	status := "error"
	if err == nil && resp != nil {
		status = strconv.Itoa(resp.StatusCode)
	}
	t.collectors.requestsTotal.WithLabelValues(method, status).Inc()
	t.collectors.requestDuration.WithLabelValues(method, status).Observe(time.Since(start).Seconds())
	return resp, err
}
