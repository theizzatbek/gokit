package redisclient

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
)

// metricsCollector bundles the redis_* collectors and pulls live
// pool stats into the gauge at scrape time. Same pattern as
// db/metrics.go — implements prometheus.Collector so we register the
// collector itself (not each individual metric) on the user's
// Registerer.
type metricsCollector struct {
	rdb *redis.Client

	commandsTotal *prometheus.CounterVec   // cmd, outcome=success|error
	cmdDuration   *prometheus.HistogramVec // cmd
	poolSize      *prometheus.GaugeVec     // state=hits|misses|idle|stale|total
}

func newMetricsCollector(reg prometheus.Registerer, rdb *redis.Client) *metricsCollector {
	mc := &metricsCollector{
		rdb: rdb,
		commandsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "redis_commands_total",
			Help: "Number of Redis commands executed, labelled by command name and outcome (success|error).",
		}, []string{"cmd", "outcome"}),
		cmdDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "redis_command_duration_seconds",
			Help:    "Redis command duration in seconds.",
			Buckets: prometheus.DefBuckets,
		}, []string{"cmd"}),
		poolSize: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "redis_pool_size_total",
			Help: "Underlying connection pool state. state=hits|misses|idle|stale|total.",
		}, []string{"state"}),
	}
	reg.MustRegister(mc)
	return mc
}

func (m *metricsCollector) Describe(ch chan<- *prometheus.Desc) {
	m.commandsTotal.Describe(ch)
	m.cmdDuration.Describe(ch)
	m.poolSize.Describe(ch)
}

func (m *metricsCollector) Collect(ch chan<- prometheus.Metric) {
	m.refreshPoolStats()
	m.commandsTotal.Collect(ch)
	m.cmdDuration.Collect(ch)
	m.poolSize.Collect(ch)
}

// observe is called from the hook for every command. cmd is the
// upstream command name (lowercased by go-redis). err is whatever
// the command returned — nil on success.
func (m *metricsCollector) observe(cmd string, elapsed time.Duration, err error) {
	if m == nil {
		return
	}
	outcome := "success"
	if err != nil && err != redis.Nil {
		// redis.Nil is the "key not found" signal, not an error in
		// the operational sense — counts as success.
		outcome = "error"
	}
	m.commandsTotal.WithLabelValues(cmd, outcome).Inc()
	m.cmdDuration.WithLabelValues(cmd).Observe(elapsed.Seconds())
}

// refreshPoolStats reads the live pool stats from go-redis and
// updates the gauge. Called on every scrape.
func (m *metricsCollector) refreshPoolStats() {
	if m.rdb == nil {
		return
	}
	s := m.rdb.PoolStats()
	m.poolSize.WithLabelValues("hits").Set(float64(s.Hits))
	m.poolSize.WithLabelValues("misses").Set(float64(s.Misses))
	m.poolSize.WithLabelValues("idle").Set(float64(s.IdleConns))
	m.poolSize.WithLabelValues("stale").Set(float64(s.StaleConns))
	m.poolSize.WithLabelValues("total").Set(float64(s.TotalConns))
}
