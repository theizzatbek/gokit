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
}

// buildConnString renders cfg as a libpq-style URL. Password and user are
// URL-escaped so special characters (slashes, at-signs) don't break parsing.
func buildConnString(cfg Config) string {
	userinfo := url.QueryEscape(cfg.User)
	if cfg.Password != "" {
		userinfo += ":" + url.QueryEscape(cfg.Password)
	}

	q := url.Values{}
	q.Set("sslmode", cfg.SSLMode)
	if cfg.AppName != "" {
		q.Set("application_name", cfg.AppName)
	}
	if cfg.ConnectTimeout > 0 {
		q.Set("connect_timeout", strconv.Itoa(int(cfg.ConnectTimeout.Seconds())))
	}

	return fmt.Sprintf("postgres://%s@%s:%d/%s?%s",
		userinfo, cfg.Host, cfg.Port, cfg.Database, q.Encode())
}
