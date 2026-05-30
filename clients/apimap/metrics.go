package apimap

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// apimapMetrics is the per-Client set of collectors built once at
// Engine.Build time when WithMetrics was supplied. Stored on *Client
// and observed at the single ep.httpClient.Do call site so Do,
// Decode, and Exchange all feed the same series without duplicating
// the wrapping logic.
//
// Labels:
//
//	client    — YAML clients[].name (one per upstream API)
//	endpoint  — clients[].endpoints[].name
//	status    — "2xx" / "3xx" / "4xx" / "5xx" / "error"
//
// status_class is preferred over the raw integer to keep label
// cardinality bounded — most operators care about success-vs-failure
// at this layer, not the precise 401 vs 403 split (that lives in the
// per-endpoint *errs.Error.Code).
//
// We register apimap_* on a registry that may ALSO carry httpc_*
// (the service-wide registry). That used to panic because earlier
// versions of apimap forwarded WithMetrics straight to the underlying
// httpc — apimap's httpc clients would then re-register httpc_*
// collectors. The current design (apimap-owned collectors, no
// pass-through) makes the panic impossible: apimap_* names are
// distinct from httpc_*.
type apimapMetrics struct {
	requestsTotal   *prometheus.CounterVec
	requestDuration *prometheus.HistogramVec
}

func newApimapMetrics(reg prometheus.Registerer) *apimapMetrics {
	m := &apimapMetrics{
		requestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "apimap",
			Name:      "requests_total",
			Help:      "Number of outbound apimap requests served per client+endpoint, bucketed by status_class.",
		}, []string{"client", "endpoint", "status"}),
		requestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "apimap",
			Name:      "request_duration_seconds",
			Help:      "Outbound apimap request latency in seconds, measured at ep.httpClient.Do (includes retries).",
			Buckets:   prometheus.DefBuckets,
		}, []string{"client", "endpoint", "status"}),
	}
	reg.MustRegister(m.requestsTotal, m.requestDuration)
	return m
}

func (m *apimapMetrics) observe(client, endpoint string, resp *http.Response, err error, dur time.Duration) {
	if m == nil {
		return
	}
	class := statusClass(resp, err)
	m.requestsTotal.WithLabelValues(client, endpoint, class).Inc()
	m.requestDuration.WithLabelValues(client, endpoint, class).Observe(dur.Seconds())
}

// statusClass folds a (response, error) outcome into the bounded set
// of labels apimap exposes. Transport errors (timeouts, connect
// failures, retry-exhausted) → "error" regardless of any partial
// response. 1xx is folded into 2xx — apimap never returns 1xx to
// callers (httpc swallows it).
func statusClass(resp *http.Response, err error) string {
	if err != nil || resp == nil {
		return "error"
	}
	switch {
	case resp.StatusCode >= 500:
		return "5xx"
	case resp.StatusCode >= 400:
		return "4xx"
	case resp.StatusCode >= 300:
		return "3xx"
	default:
		return "2xx"
	}
}
