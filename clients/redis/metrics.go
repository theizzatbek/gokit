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
// poolStatser is the subset of go-redis client interfaces that
// exposes PoolStats. *redis.Client, *redis.ClusterClient (aggregated
// across shards), and *redis.SentinelClient all satisfy it.
type poolStatser interface {
	PoolStats() *redis.PoolStats
}

type metricsCollector struct {
	rdb poolStatser

	commandsTotal    *prometheus.CounterVec   // cmd, outcome=success|error
	cmdDuration      *prometheus.HistogramVec // cmd
	poolSize         *prometheus.GaugeVec     // state=hits|misses|idle|stale|total
	connectionStatus prometheus.Gauge
}

func newMetricsCollector(reg prometheus.Registerer, rdb redis.UniversalClient) *metricsCollector {
	ps, _ := rdb.(poolStatser)
	mc := &metricsCollector{
		rdb: ps,
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
		connectionStatus: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "redis_connection_status",
			Help: "1 when the kit Redis client is connected (initial Connect ping succeeded); 0 after Close.",
		}),
	}
	reg.MustRegister(mc)
	return mc
}

func (m *metricsCollector) Describe(ch chan<- *prometheus.Desc) {
	m.commandsTotal.Describe(ch)
	m.cmdDuration.Describe(ch)
	m.poolSize.Describe(ch)
	m.connectionStatus.Describe(ch)
}

func (m *metricsCollector) Collect(ch chan<- prometheus.Metric) {
	m.refreshPoolStats()
	m.commandsTotal.Collect(ch)
	m.cmdDuration.Collect(ch)
	m.poolSize.Collect(ch)
	m.connectionStatus.Collect(ch)
}

func (m *metricsCollector) setConnectionStatus(connected bool) {
	if m == nil {
		return
	}
	if connected {
		m.connectionStatus.Set(1)
	} else {
		m.connectionStatus.Set(0)
	}
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
// updates the gauge. Called on every scrape. Cluster mode aggregates
// across every shard's pool; single / sentinel are straight reads.
func (m *metricsCollector) refreshPoolStats() {
	if m == nil || m.rdb == nil {
		return
	}
	s := m.rdb.PoolStats()
	if s == nil {
		return
	}
	m.poolSize.WithLabelValues("hits").Set(float64(s.Hits))
	m.poolSize.WithLabelValues("misses").Set(float64(s.Misses))
	m.poolSize.WithLabelValues("idle").Set(float64(s.IdleConns))
	m.poolSize.WithLabelValues("stale").Set(float64(s.StaleConns))
	m.poolSize.WithLabelValues("total").Set(float64(s.TotalConns))
}
