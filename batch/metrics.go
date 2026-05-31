package batch

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// metricsCollector bundles every batch_* series and implements
// prometheus.Collector so the caller's Registerer holds one
// collector rather than four loose vecs.
type metricsCollector struct {
	handlers        *prometheus.CounterVec // outcome=success|error
	itemsProcessed  prometheus.Counter
	handlerDuration prometheus.Histogram
	batchSize       prometheus.Histogram
}

func newMetricsCollector(reg prometheus.Registerer) *metricsCollector {
	m := &metricsCollector{
		handlers: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "batch_handlers_total",
			Help: "Number of HandlerFn invocations, by outcome (success|error).",
		}, []string{"outcome"}),
		itemsProcessed: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "batch_items_processed_total",
			Help: "Number of items accepted by Submit over the batcher's lifetime.",
		}),
		handlerDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "batch_handler_duration_seconds",
			Help:    "Wall time spent inside HandlerFn per batch.",
			Buckets: prometheus.DefBuckets,
		}),
		batchSize: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "batch_batch_size",
			Help:    "Number of items in each flushed batch.",
			Buckets: []float64{1, 10, 50, 100, 250, 500, 1000, 2500, 5000, 10000},
		}),
	}
	reg.MustRegister(m)
	return m
}

func (m *metricsCollector) Describe(ch chan<- *prometheus.Desc) {
	m.handlers.Describe(ch)
	m.itemsProcessed.Describe(ch)
	m.handlerDuration.Describe(ch)
	m.batchSize.Describe(ch)
}

func (m *metricsCollector) Collect(ch chan<- prometheus.Metric) {
	m.handlers.Collect(ch)
	m.itemsProcessed.Collect(ch)
	m.handlerDuration.Collect(ch)
	m.batchSize.Collect(ch)
}

func (m *metricsCollector) incItems() {
	if m == nil {
		return
	}
	m.itemsProcessed.Inc()
}

func (m *metricsCollector) observeHandle(err error, elapsed time.Duration, items int) {
	if m == nil {
		return
	}
	outcome := "success"
	if err != nil {
		outcome = "error"
	}
	m.handlers.WithLabelValues(outcome).Inc()
	m.handlerDuration.Observe(elapsed.Seconds())
	m.batchSize.Observe(float64(items))
}
