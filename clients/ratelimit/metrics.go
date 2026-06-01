package ratelimit

import "github.com/prometheus/client_golang/prometheus"

type metrics struct {
	requests *prometheus.CounterVec
	duration prometheus.Histogram
	errors   prometheus.Counter
}

func newMetrics(reg prometheus.Registerer) *metrics {
	m := &metrics{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ratelimit_requests_total",
			Help: "Rate-limit Allow checks by outcome.",
		}, []string{"outcome"}),
		duration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "ratelimit_allow_duration_seconds",
			Help:    "Latency of ratelimit.Allow round-trips.",
			Buckets: []float64{.0005, .001, .0025, .005, .01, .025, .05, .1, .25, .5, 1},
		}),
		errors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "ratelimit_backend_errors_total",
			Help: "Allow calls that failed to reach Redis.",
		}),
	}
	reg.MustRegister(m.requests, m.duration, m.errors)
	return m
}

func (m *metrics) record(outcome string) {
	if m == nil {
		return
	}
	m.requests.WithLabelValues(outcome).Inc()
}

func (m *metrics) recordErr() {
	if m == nil {
		return
	}
	m.errors.Inc()
}

func (m *metrics) observe(seconds float64) {
	if m == nil {
		return
	}
	m.duration.Observe(seconds)
}
