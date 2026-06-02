package breaker

import "github.com/prometheus/client_golang/prometheus"

// metricsCollector bundles every breaker_* series and implements
// prometheus.Collector so the caller's Registerer holds one collector
// rather than four loose vecs.
type metricsCollector struct {
	name          string
	state         prometheus.Gauge
	transitions   *prometheus.CounterVec // labels: from, to
	shortCircuits prometheus.Counter
	requests      *prometheus.CounterVec // label: outcome=success|failure|short_circuit
}

func newMetricsCollector(reg prometheus.Registerer, name string) *metricsCollector {
	m := &metricsCollector{
		name: name,
		state: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "breaker_state",
			Help:        "Current breaker state: 0=closed, 1=open, 2=half_open.",
			ConstLabels: prometheus.Labels{"name": name},
		}),
		transitions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "breaker_transitions_total",
			Help:        "Number of state transitions, by from/to state.",
			ConstLabels: prometheus.Labels{"name": name},
		}, []string{"from", "to"}),
		shortCircuits: prometheus.NewCounter(prometheus.CounterOpts{
			Name:        "breaker_short_circuits_total",
			Help:        "Number of Allow() calls that were short-circuited by an open breaker.",
			ConstLabels: prometheus.Labels{"name": name},
		}),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "breaker_requests_total",
			Help:        "Per-outcome request counter (success|failure|short_circuit).",
			ConstLabels: prometheus.Labels{"name": name},
		}, []string{"outcome"}),
	}
	reg.MustRegister(m)
	return m
}

func (m *metricsCollector) Describe(ch chan<- *prometheus.Desc) {
	m.state.Describe(ch)
	m.transitions.Describe(ch)
	m.shortCircuits.Describe(ch)
	m.requests.Describe(ch)
}

func (m *metricsCollector) Collect(ch chan<- prometheus.Metric) {
	m.state.Collect(ch)
	m.transitions.Collect(ch)
	m.shortCircuits.Collect(ch)
	m.requests.Collect(ch)
}

func (m *metricsCollector) setState(s State) {
	if m == nil {
		return
	}
	m.state.Set(float64(s))
}

func (m *metricsCollector) recordTransition(from, to State) {
	if m == nil {
		return
	}
	m.transitions.WithLabelValues(from.String(), to.String()).Inc()
}

func (m *metricsCollector) incShortCircuit() {
	if m == nil {
		return
	}
	m.shortCircuits.Inc()
	m.requests.WithLabelValues("short_circuit").Inc()
}

func (m *metricsCollector) incOutcome(success bool) {
	if m == nil {
		return
	}
	outcome := "success"
	if !success {
		outcome = "failure"
	}
	m.requests.WithLabelValues(outcome).Inc()
}
