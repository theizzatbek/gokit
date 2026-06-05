package db

import "github.com/jackc/pgx/v5/pgxpool"

// PoolStats is the per-pool snapshot returned by [DB.Stats]. Mirrors
// pgxpool.Stat with the kit's stable Name labelling so admin
// dashboards can render both primary + every replica with one
// projection.
type PoolStats struct {
	Name     string // "primary" | "standby" | "standby-N"
	Acquired int32  // currently checked-out connections
	Idle     int32  // open but unused connections
	Max      int32  // configured max pool size
	Total    int32  // open connections (Acquired + Idle)

	// Healthy + LagSeconds are kit-tracked state (lag-polling
	// goroutine output). For the primary pool both fields are zero-
	// valued; only standby entries carry meaningful data.
	Healthy    bool
	LagSeconds float64
}

// Stats is the global snapshot returned by [DB.Stats]. Cheap to
// compute (one pgxpool.Stat call per pool + a few atomic loads); safe
// to expose from a /admin endpoint.
//
// The kit does NOT cache the snapshot — call sparingly under heavy
// scrape pressure (every call walks every pool).
type Stats struct {
	Primary     PoolStats
	Replicas    []PoolStats
	HasReplicas bool
}

// Stats returns the kit-wide pool snapshot — primary + every read
// replica. Use from /admin endpoints to render a small pool-pressure
// dashboard without scraping the Prometheus registry.
//
// Replicas[i] is in the same order as [DB.ReadPools]. The HasReplicas
// flag is a convenience for templates that want to omit a "replicas"
// section when none are configured.
func (d *DB) Stats() Stats {
	if d == nil || d.pool == nil {
		return Stats{}
	}
	out := Stats{
		Primary:     poolStatToStats("primary", d.pool, true, 0),
		HasReplicas: len(d.readPools) > 0,
	}
	if len(d.readPools) > 0 {
		out.Replicas = make([]PoolStats, 0, len(d.readPools))
		for _, e := range d.readPools {
			out.Replicas = append(out.Replicas,
				poolStatToStats(e.name, e.pool, e.healthy.Load(),
					lagMillisToSeconds(e.lagMillis.Load())))
		}
	}
	return out
}

// poolStatToStats projects the pgx-native stat snapshot into the
// kit-stable PoolStats shape.
func poolStatToStats(name string, p *pgxpool.Pool, healthy bool, lag float64) PoolStats {
	s := p.Stat()
	return PoolStats{
		Name:       name,
		Acquired:   s.AcquiredConns(),
		Idle:       s.IdleConns(),
		Max:        s.MaxConns(),
		Total:      s.TotalConns(),
		Healthy:    healthy,
		LagSeconds: lag,
	}
}
