package cronmap

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// metricsCollector bundles every cronmap_* series and implements
// prometheus.Collector so the caller's Registerer holds one
// collector rather than four loose vecs.
type metricsCollector struct {
	runs           *prometheus.CounterVec   // labels: name, outcome
	duration       *prometheus.HistogramVec // label: name
	singletonSkips *prometheus.CounterVec   // label: name
	jobsGauge      prometheus.Gauge
}

// newMetricsCollector returns nil when reg is nil (zero footprint).
// All collector methods on a nil receiver are safe no-ops so call
// sites do not need to nil-check.
func newMetricsCollector(reg prometheus.Registerer) *metricsCollector {
	if reg == nil {
		return nil
	}
	m := &metricsCollector{
		runs: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cronmap_runs_total",
			Help: "Per-job invocation outcomes (success|failure|timeout).",
		}, []string{"name", "outcome"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "cronmap_run_duration_seconds",
			Help:    "Wall time spent inside each cron job invocation.",
			Buckets: prometheus.DefBuckets,
		}, []string{"name"}),
		singletonSkips: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cronmap_singleton_skipped_total",
			Help: "Number of ticks skipped because the singleton lock was held by another instance.",
		}, []string{"name"}),
		jobsGauge: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "cronmap_jobs",
			Help: "Number of registered cron jobs (set once at Build).",
		}),
	}
	reg.MustRegister(m)
	return m
}

func (m *metricsCollector) Describe(ch chan<- *prometheus.Desc) {
	m.runs.Describe(ch)
	m.duration.Describe(ch)
	m.singletonSkips.Describe(ch)
	m.jobsGauge.Describe(ch)
}

func (m *metricsCollector) Collect(ch chan<- prometheus.Metric) {
	m.runs.Collect(ch)
	m.duration.Collect(ch)
	m.singletonSkips.Collect(ch)
	m.jobsGauge.Collect(ch)
}

func (m *metricsCollector) observeRun(name, outcome string, dur time.Duration) {
	if m == nil {
		return
	}
	m.runs.WithLabelValues(name, outcome).Inc()
	m.duration.WithLabelValues(name).Observe(dur.Seconds())
}

func (m *metricsCollector) incSingletonSkip(name string) {
	if m == nil {
		return
	}
	m.singletonSkips.WithLabelValues(name).Inc()
}

func (m *metricsCollector) setJobs(n float64) {
	if m == nil {
		return
	}
	m.jobsGauge.Set(n)
}
