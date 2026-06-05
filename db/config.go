package db

import (
	"fmt"
	"net/url"
	"strconv"
	"time"
)

// Config carries the connection parameters and pool tuning for db.Connect.
// Field tags target caarlos0/env/v11. Callers typically do:
//
//	var cfg struct {
//	    DB db.Config `envPrefix:"DB_"`
//	}
//	if err := env.Parse(&cfg); err != nil { ... }
type Config struct {
	// URL is the full postgres connection string. When set, all
	// connection-identity fields (Host, Port, User, Password, Database,
	// SSLMode) are ignored. K8s-native pattern:
	//
	//   DB_URL=postgres://app:pass@postgres-svc.default:5432/appdb?sslmode=disable
	//
	// Multi-host failover (pgx native):
	//
	//   DB_URL=postgres://app:pass@h1,h2,h3:5432/appdb
	//
	// AppName and ConnectTimeout are still merged into the URL as query
	// parameters when they are not already present — only the identity
	// fields are fully ignored.
	URL string `env:"URL"`

	// HasReadReplica enables a second internal pgxpool against the same
	// URL with target_session_attrs=standby. ReadQuery / ReadQueryRow
	// route to it; everything else uses the primary pool. Requires
	// PostgreSQL 14+. Default false; ReadQuery falls back to the primary
	// pool in that case.
	//
	// Ignored when ReadURLs is non-empty — ReadURLs is the more explicit
	// multi-replica path and takes precedence.
	HasReadReplica bool `env:"HAS_READ_REPLICA"`

	// ReadURLs is the list of dedicated read-replica connection strings.
	// When set, the kit opens one pgxpool per URL; ReadQuery / ReadQueryRow
	// round-robin (or pick at random, see ReadStrategy) across the
	// healthy entries.
	//
	// Each URL may carry its own credentials, host, or sslmode — useful
	// for geo-distributed replicas, dedicated reporting replicas, or
	// per-replica role separation. URLs with NO target_session_attrs
	// query parameter get "standby" appended automatically; pass the
	// parameter explicitly to override (e.g. analytics replicas using
	// target_session_attrs=any).
	//
	// Env: DB_READ_URLS=postgres://a,postgres://b,postgres://c (comma-
	// separated; empty entries are dropped).
	ReadURLs []string `env:"READ_URLS" envSeparator:","`

	// ReadStrategy picks the routing policy across configured ReadURLs.
	// "round_robin" (default) cycles atomically; "random" picks uniformly.
	// Ignored when at most one read pool is configured.
	ReadStrategy string `env:"READ_STRATEGY" envDefault:"round_robin"`

	Host     string `env:"HOST"          envDefault:"localhost"`
	Port     int    `env:"PORT"          envDefault:"5432"`
	User     string `env:"USER,required"`
	Password string `env:"PASSWORD"`
	Database string `env:"NAME,required"`
	SSLMode  string `env:"SSLMODE"       envDefault:"disable"`
	AppName  string `env:"APP_NAME"`

	MaxConns        int32         `env:"MAX_CONNS"     envDefault:"10"`
	MinConns        int32         `env:"MIN_CONNS"     envDefault:"0"`
	MaxConnLifetime time.Duration `env:"MAX_LIFETIME"  envDefault:"1h"`
	MaxConnIdle     time.Duration `env:"MAX_IDLE"      envDefault:"30m"`
	ConnectTimeout  time.Duration `env:"CONN_TIMEOUT"  envDefault:"5s"`

	// ConnectMaxRetries caps the number of retry attempts during the
	// initial Connect. 0 = no retry (1 attempt). N>0 = N additional
	// attempts after the first failure. Service.New auto-defaults to 5
	// when this is 0; pass -1 via env to disable that injection.
	ConnectMaxRetries int `env:"CONNECT_MAX_RETRIES"`

	// ConnectBackoffBase is the initial wait between connect attempts.
	// Doubles each attempt, capped at ConnectBackoffMax. Default 0
	// (service injects 1s).
	ConnectBackoffBase time.Duration `env:"CONNECT_BACKOFF_BASE"`

	// ConnectBackoffMax caps the per-attempt wait. Default 0 (service
	// injects 16s).
	ConnectBackoffMax time.Duration `env:"CONNECT_BACKOFF_MAX"`

	// LagBudget caps the acceptable replication lag on a read replica
	// before the router skips it. Equivalent to wiring
	// [WithReadLagBudget] programmatically — Config-derived value
	// applies BEFORE any user-supplied option so explicit code wins.
	//
	// Only meaningful with at least one replica AND with LagPolling
	// also configured (lag is only tracked when the poller runs).
	// 0 = disabled (all healthy replicas eligible, default behaviour).
	LagBudget time.Duration `env:"LAG_BUDGET"`

	// LagPollInterval enables the background replication-lag poller.
	// Equivalent to wiring [WithReplicaLagPolling] programmatically.
	// 0 = disabled.
	LagPollInterval time.Duration `env:"LAG_POLL_INTERVAL"`

	// LagPollThreshold is the per-replica WARN threshold consumed by
	// the lag-polling goroutine. Without it the gauge still updates
	// but no log line is emitted. Only meaningful with
	// LagPollInterval > 0.
	LagPollThreshold time.Duration `env:"LAG_POLL_THRESHOLD"`
}

// buildPgxURL renders cfg as a libpq-style URL suitable for pgxpool.ParseConfig.
//
// When cfg.URL is non-empty it is used verbatim as the base; otherwise the URL
// is assembled from Host/Port/User/Password/Database/SSLMode with AppName and
// ConnectTimeout injected as query parameters.
//
// tsa, when non-empty, is injected as the target_session_attrs query parameter
// unless the user has already set it in cfg.URL. The primary pool passes
// "read-write" to defend against a multi-host URL silently landing on a
// standby; a read-replica pool passes "standby". Empty tsa skips injection.
func buildPgxURL(cfg Config, tsa string) (string, error) {
	var raw string
	if cfg.URL != "" {
		raw = cfg.URL
	} else {
		raw = assembleURL(cfg)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	q := u.Query()
	if cfg.AppName != "" && q.Get("application_name") == "" {
		q.Set("application_name", cfg.AppName)
	}
	if cfg.ConnectTimeout > 0 && q.Get("connect_timeout") == "" {
		q.Set("connect_timeout", strconv.Itoa(int(cfg.ConnectTimeout.Seconds())))
	}
	if tsa != "" && q.Get("target_session_attrs") == "" {
		q.Set("target_session_attrs", tsa)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// assembleURL renders the non-URL fields into a postgres:// string.
func assembleURL(cfg Config) string {
	var userinfo *url.Userinfo
	if cfg.Password != "" {
		userinfo = url.UserPassword(cfg.User, cfg.Password)
	} else {
		userinfo = url.User(cfg.User)
	}
	q := url.Values{}
	q.Set("sslmode", cfg.SSLMode)
	return fmt.Sprintf("postgres://%s@%s:%d/%s?%s",
		userinfo.String(), cfg.Host, cfg.Port, cfg.Database, q.Encode())
}
