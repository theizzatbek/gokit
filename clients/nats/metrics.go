package natsclient

import (
	"github.com/prometheus/client_golang/prometheus"
)

type metricsCollector struct {
	publishTotal     *prometheus.CounterVec
	publishDuration  *prometheus.HistogramVec
	handlerTotal     *prometheus.CounterVec
	handlerDuration  *prometheus.HistogramVec
	inFlight         *prometheus.GaugeVec
	connectionStatus prometheus.Gauge
}

func newMetricsCollector(reg prometheus.Registerer) *metricsCollector {
	m := &metricsCollector{
		publishTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "nats_publish_total", Help: "publish attempts",
		}, []string{"subject", "outcome"}),
		publishDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "nats_publish_duration_seconds", Help: "publish-ack latency (JetStream only)",
		}, []string{"subject"}),
		handlerTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "nats_handler_total", Help: "handler invocations",
		}, []string{"subject", "outcome"}),
		handlerDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "nats_handler_duration_seconds", Help: "handler execution time",
		}, []string{"subject"}),
		inFlight: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "nats_in_flight", Help: "currently-running handlers",
		}, []string{"subject"}),
		connectionStatus: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "nats_connection_status", Help: "1=connected 0=disconnected",
		}),
	}
	if reg != nil {
		reg.MustRegister(m.publishTotal, m.publishDuration, m.handlerTotal,
			m.handlerDuration, m.inFlight, m.connectionStatus)
	}
	m.connectionStatus.Set(1) // only built after successful Connect
	return m
}

func (m *metricsCollector) IncPublishSuccess(subject string) {
	m.publishTotal.WithLabelValues(subject, "success").Inc()
}
func (m *metricsCollector) IncPublishError(subject string) {
	m.publishTotal.WithLabelValues(subject, "error").Inc()
}
func (m *metricsCollector) ObservePublish(subject string, seconds float64) {
	m.publishDuration.WithLabelValues(subject).Observe(seconds)
}
func (m *metricsCollector) IncHandlerSuccess(subject string) {
	m.handlerTotal.WithLabelValues(subject, "success").Inc()
}
func (m *metricsCollector) IncHandlerError(subject string) {
	m.handlerTotal.WithLabelValues(subject, "error").Inc()
}
func (m *metricsCollector) IncHandlerDecodeError(subject string) {
	m.handlerTotal.WithLabelValues(subject, "decode_error").Inc()
}
func (m *metricsCollector) ObserveHandler(subject string, seconds float64) {
	m.handlerDuration.WithLabelValues(subject).Observe(seconds)
}
func (m *metricsCollector) IncInFlight(subject string) { m.inFlight.WithLabelValues(subject).Inc() }
func (m *metricsCollector) DecInFlight(subject string) { m.inFlight.WithLabelValues(subject).Dec() }
func (m *metricsCollector) SetConnectionStatus(connected bool) {
	if connected {
		m.connectionStatus.Set(1)
	} else {
		m.connectionStatus.Set(0)
	}
}
