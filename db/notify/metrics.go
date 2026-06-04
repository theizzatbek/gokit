package notify

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// outcome labels for notify_notifications_total.
const (
	outcomeOK      = "ok"
	outcomeHandler = "handler_error"
)

// metricsCollector bundles every notify_* series and implements
// prometheus.Collector so the registry holds one entry per Notifier.
type metricsCollector struct {
	notifications   *prometheus.CounterVec // labels: channel, outcome
	reconnects      prometheus.Counter
	handlerDuration *prometheus.HistogramVec // label: channel
}

func newMetricsCollector(reg prometheus.Registerer) *metricsCollector {
	m := &metricsCollector{
		notifications: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "notify_notifications_total",
			Help: "Notifications received from LISTEN, by channel and handler outcome.",
		}, []string{"channel", "outcome"}),
		reconnects: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "notify_reconnects_total",
			Help: "Times the listen loop had to acquire a fresh connection after a drop.",
		}),
		handlerDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "notify_handler_duration_seconds",
			Help:    "Per-notification handler wall time.",
			Buckets: prometheus.DefBuckets,
		}, []string{"channel"}),
	}
	reg.MustRegister(m)
	return m
}

func (m *metricsCollector) Describe(ch chan<- *prometheus.Desc) {
	m.notifications.Describe(ch)
	m.reconnects.Describe(ch)
	m.handlerDuration.Describe(ch)
}

func (m *metricsCollector) Collect(ch chan<- prometheus.Metric) {
	m.notifications.Collect(ch)
	m.reconnects.Collect(ch)
	m.handlerDuration.Collect(ch)
}

func (m *metricsCollector) recordNotification(channel, outcome string, d time.Duration) {
	if m == nil {
		return
	}
	m.notifications.WithLabelValues(channel, outcome).Inc()
	m.handlerDuration.WithLabelValues(channel).Observe(d.Seconds())
}

func (m *metricsCollector) recordReconnect() {
	if m == nil {
		return
	}
	m.reconnects.Inc()
}
