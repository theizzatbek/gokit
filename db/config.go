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
	URL string `env:"URL"`

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
}

// buildPgxURL renders cfg as a libpq-style URL suitable for pgxpool.ParseConfig.
//
// When cfg.URL is non-empty it is used verbatim as the base; otherwise the URL
// is assembled from Host/Port/User/Password/Database/SSLMode with AppName and
// ConnectTimeout injected as query parameters.
func buildPgxURL(cfg Config) (string, error) {
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
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// assembleURL renders the non-URL fields into a postgres:// string.
func assembleURL(cfg Config) string {
	userinfo := url.QueryEscape(cfg.User)
	if cfg.Password != "" {
		userinfo += ":" + url.QueryEscape(cfg.Password)
	}
	q := url.Values{}
	q.Set("sslmode", cfg.SSLMode)
	return fmt.Sprintf("postgres://%s@%s:%d/%s?%s",
		userinfo, cfg.Host, cfg.Port, cfg.Database, q.Encode())
}
