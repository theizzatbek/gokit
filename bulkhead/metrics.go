package bulkhead

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// metricsCollector bundles every bulkhead_* series and implements
// prometheus.Collector so the caller's Registerer holds one collector
// rather than six loose vecs.
type metricsCollector struct {
	name        string
	inFlight    prometheus.GaugeFunc
	waiting     prometheus.GaugeFunc
	capacity    prometheus.GaugeFunc
	acquires    *prometheus.CounterVec   // label: outcome
	waitTime    *prometheus.HistogramVec // label: outcome
	callLatency prometheus.Histogram     // in-flight duration on release
}

// newMetricsCollector registers a fresh collector set on reg. The three
// GaugeFunc closures snapshot the current bulkhead state under mu on
// every scrape.
func newMetricsCollector(reg prometheus.Registerer, name string,
	inFlightFn, waitingFn, capacityFn func() float64) *metricsCollector {
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
		capacity: prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name:        "bulkhead_capacity",
			Help:        "Current MaxConcurrent target — moves over time when WithAdaptive is set.",
			ConstLabels: labels,
		}, capacityFn),
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
		callLatency: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:        "bulkhead_call_latency_seconds",
			Help:        "Wall time slots stayed in-flight (release - Acquire).",
			ConstLabels: labels,
			Buckets:     prometheus.DefBuckets,
		}),
	}
	reg.MustRegister(m)
	return m
}

func (m *metricsCollector) Describe(ch chan<- *prometheus.Desc) {
	m.inFlight.Describe(ch)
	m.waiting.Describe(ch)
	m.capacity.Describe(ch)
	m.acquires.Describe(ch)
	m.waitTime.Describe(ch)
	m.callLatency.Describe(ch)
}

func (m *metricsCollector) Collect(ch chan<- prometheus.Metric) {
	m.inFlight.Collect(ch)
	m.waiting.Collect(ch)
	m.capacity.Collect(ch)
	m.acquires.Collect(ch)
	m.waitTime.Collect(ch)
	m.callLatency.Collect(ch)
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

// observeCallLatency records one in-flight duration (Acquire → release).
func (m *metricsCollector) observeCallLatency(dur time.Duration) {
	if m == nil {
		return
	}
	m.callLatency.Observe(dur.Seconds())
}
