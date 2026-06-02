package bulkhead

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// metricsCollector bundles every bulkhead_* series and implements
// prometheus.Collector so the caller's Registerer holds one collector
// rather than four loose vecs.
type metricsCollector struct {
	name     string
	inFlight prometheus.GaugeFunc
	waiting  prometheus.GaugeFunc
	acquires *prometheus.CounterVec   // label: outcome
	waitTime *prometheus.HistogramVec // label: outcome
}

// newMetricsCollector registers a fresh collector set on reg, with
// inFlightFn / waitingFn called on every scrape (cheap snapshot — they
// read len(chan) / atomic.Int64).
func newMetricsCollector(reg prometheus.Registerer, name string, inFlightFn, waitingFn func() float64) *metricsCollector {
	labels := prometheus.Labels{"name": name}
	m := &metricsCollector{
		name: name,
		inFlight: prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name:        "bulkhead_in_flight",
			Help:        "Current number of in-flight calls holding a slot.",
			ConstLabels: labels,
		}, inFlightFn),
		waiting: prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name:        "bulkhead_waiting",
			Help:        "Current number of callers queued waiting for a slot.",
			ConstLabels: labels,
		}, waitingFn),
		acquires: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "bulkhead_acquires_total",
			Help:        "Acquire outcomes (ok | full | ctx_canceled | queue_timeout).",
			ConstLabels: labels,
		}, []string{"outcome"}),
		waitTime: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "bulkhead_wait_duration_seconds",
			Help:        "Wall time spent in Acquire before resolution, by outcome.",
			ConstLabels: labels,
			Buckets:     prometheus.DefBuckets,
		}, []string{"outcome"}),
	}
	reg.MustRegister(m)
	return m
}

func (m *metricsCollector) Describe(ch chan<- *prometheus.Desc) {
	m.inFlight.Describe(ch)
	m.waiting.Describe(ch)
	m.acquires.Describe(ch)
	m.waitTime.Describe(ch)
}

func (m *metricsCollector) Collect(ch chan<- prometheus.Metric) {
	m.inFlight.Collect(ch)
	m.waiting.Collect(ch)
	m.acquires.Collect(ch)
	m.waitTime.Collect(ch)
}

// observe records an Acquire outcome + the wait duration that produced
// it. nil receiver is a no-op so adapters need no checks.
func (m *metricsCollector) observe(outcome string, wait time.Duration) {
	if m == nil {
		return
	}
	m.acquires.WithLabelValues(outcome).Inc()
	m.waitTime.WithLabelValues(outcome).Observe(wait.Seconds())
}
