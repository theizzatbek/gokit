package db

import (
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

// poolStat is the subset of pgxpool.Stat we expose as gauges. Defined
// separately so unit tests can populate it without spinning up a pool.
type poolStat struct {
	Acquired int32
	Idle     int32
	Max      int32
	Total    int32
}

// metricsCollector implements prometheus.Collector. Gauges are refreshed
// from the underlying pgxpool on every scrape — no goroutine, no polling.
// The duration histogram is observed inline by the tracer.
type metricsCollector struct {
	pool     *pgxpool.Pool
	poolSize *prometheus.GaugeVec
	duration *prometheus.HistogramVec
}

func newMetricsCollector(reg prometheus.Registerer) *metricsCollector {
	mc := &metricsCollector{
		poolSize: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "db_pool_size_total",
			Help: "pgx pool size, labelled by state (acquired|idle|max|total).",
		}, []string{"state"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "db_query_duration_seconds",
			Help:    "Histogram of pgx query durations.",
			Buckets: prometheus.DefBuckets,
		}, []string{"outcome"}),
	}
	reg.MustRegister(mc)
	return mc
}

// Describe implements prometheus.Collector.
func (m *metricsCollector) Describe(ch chan<- *prometheus.Desc) {
	m.poolSize.Describe(ch)
	m.duration.Describe(ch)
}

// Collect implements prometheus.Collector. Refreshes pool gauges from the
// underlying pgxpool on every scrape, then delegates emission.
func (m *metricsCollector) Collect(ch chan<- prometheus.Metric) {
	m.refreshPoolStats()
	m.poolSize.Collect(ch)
	m.duration.Collect(ch)
}

func (m *metricsCollector) attach(p *pgxpool.Pool) { m.pool = p }

func (m *metricsCollector) observe(elapsed time.Duration, err error) {
	outcome := "success"
	if err != nil {
		outcome = "error"
	}
	m.duration.WithLabelValues(outcome).Observe(elapsed.Seconds())
}

func (m *metricsCollector) setPoolStat(s poolStat) {
	m.poolSize.WithLabelValues("acquired").Set(float64(s.Acquired))
	m.poolSize.WithLabelValues("idle").Set(float64(s.Idle))
	m.poolSize.WithLabelValues("max").Set(float64(s.Max))
	m.poolSize.WithLabelValues("total").Set(float64(s.Total))
}

func (m *metricsCollector) refreshPoolStats() {
	if m.pool == nil {
		return
	}
	s := m.pool.Stat()
	m.setPoolStat(poolStat{
		Acquired: s.AcquiredConns(),
		Idle:     s.IdleConns(),
		Max:      s.MaxConns(),
		Total:    s.TotalConns(),
	})
}
