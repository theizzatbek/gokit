package service

import (
	"time"

	"github.com/theizzatbek/gokit/clients/httpc"
	"github.com/theizzatbek/gokit/db"
	xerrs "github.com/theizzatbek/gokit/errs"
)

// Config is the env-driven service configuration. Compose into your own
// app config via embedding to add app-specific fields:
//
//	type MyConfig struct {
//	    service.Config
//	    MyField string `env:"MY_FIELD"`
//	}
type Config struct {
	Service ServiceConfig `envPrefix:""`
	DB      db.Config     `envPrefix:"DB_"`
	Auth    AuthConfig    `envPrefix:"AUTH_"`
	NATS    NATSConfig    `envPrefix:"NATS_"`
	HTTPC   httpc.Config  `envPrefix:"HTTPC_"`
	APIMap  APIMapConfig  `envPrefix:"APIMAP_"`
}

// ServiceConfig — server + logging knobs.
type ServiceConfig struct {
	Addr      string `env:"ADDR"       envDefault:":3000"`
	LogLevel  string `env:"LOG_LEVEL"  envDefault:"info"`
	LogFormat string `env:"LOG_FORMAT" envDefault:"json"` // json | text
}

// AuthConfig — JWT signing material + TTLs. PrivateKeyPEM is the
// opt-in trigger; empty means "no auth in this service".
type AuthConfig struct {
	PrivateKeyPEM string        `env:"PRIVATE_KEY_PEM"`
	KID           string        `env:"KID"         envDefault:"k1"`
	Issuer        string        `env:"ISSUER"      envDefault:"gokit"`
	AccessTTL     time.Duration `env:"ACCESS_TTL"  envDefault:"15m"`
	RefreshTTL    time.Duration `env:"REFRESH_TTL" envDefault:"720h"` // 30d
}

// NATSConfig — URL is the opt-in trigger.
type NATSConfig struct {
	URL  string `env:"URL"`
	Name string `env:"NAME"`
}

// APIMapConfig — Path to clients.yaml is the opt-in trigger.
type APIMapConfig struct {
	Path string `env:"PATH"`
}

// Validate cross-subsystem invariants. Per-subsystem validation
// (e.g. db.Config.User required) is done at Connect time.
func (c Config) Validate() error {
	if c.Auth.PrivateKeyPEM != "" && c.DB.User == "" {
		return xerrs.Validation(CodeAuthNeedsDB,
			"service: Auth.PrivateKeyPEM requires DB (refreshpg store needs a Querier)")
	}
	return nil
}
