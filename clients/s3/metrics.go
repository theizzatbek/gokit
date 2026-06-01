package s3client

import "github.com/prometheus/client_golang/prometheus"

// metricsCollector bundles the Prometheus collectors. nil-safe on
// every record method so the hot path stays zero-cost when metrics
// are off.
type metricsCollector struct {
	opsTotal   *prometheus.CounterVec
	opDuration *prometheus.HistogramVec
	bytesTotal *prometheus.CounterVec
}

func newMetricsCollector(reg prometheus.Registerer) *metricsCollector {
	if reg == nil {
		return nil
	}
	m := &metricsCollector{
		opsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "s3_operations_total",
				Help: "S3 client operations by op and outcome.",
			},
			[]string{"op", "outcome"},
		),
		opDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "s3_operation_duration_seconds",
				Help:    "S3 client operation wall time.",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"op"},
		),
		bytesTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "s3_bytes_transferred_total",
				Help: "Bytes transferred to / from S3.",
			},
			[]string{"direction"},
		),
	}
	reg.MustRegister(m.opsTotal, m.opDuration, m.bytesTotal)
	return m
}

func (m *metricsCollector) record(op, outcome string) {
	if m == nil {
		return
	}
	m.opsTotal.WithLabelValues(op, outcome).Inc()
}

func (m *metricsCollector) observe(op string, seconds float64) {
	if m == nil {
		return
	}
	m.opDuration.WithLabelValues(op).Observe(seconds)
}

func (m *metricsCollector) bytes(direction string, n int64) {
	if m == nil || n <= 0 {
		return
	}
	m.bytesTotal.WithLabelValues(direction).Add(float64(n))
}
