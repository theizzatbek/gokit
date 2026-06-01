package service

import (
	"time"

	"github.com/theizzatbek/gokit/clients/httpc"
	s3client "github.com/theizzatbek/gokit/clients/s3"
	"github.com/theizzatbek/gokit/db"
	xerrs "github.com/theizzatbek/gokit/errs"
)

// Default YAML file paths used by service subsystems when Enabled is set
// but no explicit Path override is supplied.
const (
	DefaultAPIMapPath             = "clients.yaml"
	DefaultNATSMapSubscribersPath = "subscribers.yaml"
	DefaultNATSMapPublishersPath  = "publishers.yaml"
	DefaultRoutesPath             = "routes.yaml"
)

// Config is the env-driven service configuration. Compose into your own
// app config via embedding to add app-specific fields:
//
//	type MyConfig struct {
//	    service.Config
//	    MyField string `env:"MY_FIELD"`
//	}
type Config struct {
	Service ServiceConfig   `envPrefix:""`
	DB      db.Config       `envPrefix:"DB_"`
	Auth    AuthConfig      `envPrefix:"AUTH_"`
	NATS    NATSConfig      `envPrefix:"NATS_"`
	NATSMap NATSMapConfig   `envPrefix:"NATSMAP_"`
	HTTPC   httpc.Config    `envPrefix:"HTTPC_"`
	APIMap  APIMapConfig    `envPrefix:"APIMAP_"`
	Routes  RoutesConfig    `envPrefix:"ROUTES_"`
	Redis   RedisConfig     `envPrefix:"REDIS_"`
	S3      s3client.Config `envPrefix:"S3_"`
}

// ServiceConfig — server + logging knobs.
//
// NodeName identifies the running instance in multi-node deployments;
// it defaults to os.Hostname() when unset and flows to
// natsclient.Config.Name (when NATS.Name is not explicit) and to slog
// default attrs as "node". ServerGroup labels a cluster of nodes that
// share work via the same queue groups; when set, natsmap auto-derived
// subscriber queue groups are suffixed with this value and the logger
// gains a "server_group" default attr.
type ServiceConfig struct {
	Addr        string `env:"ADDR"         envDefault:":3000"`
	LogLevel    string `env:"LOG_LEVEL"    envDefault:"info"`
	LogFormat   string `env:"LOG_FORMAT"   envDefault:"json"` // json | text
	NodeName    string `env:"NODE_NAME"`
	ServerGroup string `env:"SERVER_GROUP"`

	// ConfigsDir is the directory the kit looks in for every
	// default-named YAML (routes.yaml, clients.yaml, subscribers.yaml,
	// publishers.yaml). Empty (default) preserves the current
	// CWD-based lookup. When set, e.g. "configs", the kit reads
	// configs/routes.yaml, configs/clients.yaml, etc.
	//
	// Per-subsystem `Path` overrides (Routes.Path, APIMap.Path,
	// NATSMap.SubscribersPath, …) are taken as operator-supplied
	// literal paths and are NOT prefixed — keeping the override
	// channel transparent.
	ConfigsDir string `env:"CONFIGS_DIR"`

	// Env labels the runtime environment ("dev", "staging",
	// "prod"). [WithDevMode] uses this as the gate for mounting
	// dev-only inspectors — non-"dev" values disable mounting
	// even when the option was passed.
	Env string `env:"ENV"`
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
	URL                string        `env:"URL"`
	Name               string        `env:"NAME"`
	ConnectMaxRetries  int           `env:"CONNECT_MAX_RETRIES"`
	ConnectBackoffBase time.Duration `env:"CONNECT_BACKOFF_BASE"`
	ConnectBackoffMax  time.Duration `env:"CONNECT_BACKOFF_MAX"`
}

// NATSMapConfig — Enabled (or any *Path override) triggers natsmap
// auto-build. When Enabled is true and no override is set, the default
// paths (DefaultNATSMapSubscribersPath / DefaultNATSMapPublishersPath)
// are used; missing default files are skipped silently (supports
// publish-only / subscribe-only services). Override paths are strict —
// a missing file produces CodeNATSMapYAMLNotFound. Requires NATS.
type NATSMapConfig struct {
	Enabled         bool   `env:"ENABLED"`
	SubscribersPath string `env:"SUBSCRIBERS_PATH"`
	PublishersPath  string `env:"PUBLISHERS_PATH"`
}

// RedisConfig — URL is the opt-in trigger. Empty URL leaves
// svc.Redis nil. Retry semantics mirror NATSConfig / DB.Config:
// ConnectMaxRetries==0 → service auto-injects 5 unless
// WithoutConnectRetry is passed; pass -1 to disable explicitly.
type RedisConfig struct {
	URL                string        `env:"URL"`
	ConnectMaxRetries  int           `env:"CONNECT_MAX_RETRIES"`
	ConnectBackoffBase time.Duration `env:"CONNECT_BACKOFF_BASE"`
	ConnectBackoffMax  time.Duration `env:"CONNECT_BACKOFF_MAX"`
}

// APIMapConfig — Enabled (or Path override) triggers apimap auto-build.
type APIMapConfig struct {
	Enabled bool   `env:"ENABLED"`
	Path    string `env:"PATH"`
}

// RoutesConfig — Enabled (or Path override) triggers routes auto-load
// in svc.Run, after user-side RegisterHandler calls and before
// engine.Mount.
type RoutesConfig struct {
	Enabled bool   `env:"ENABLED"`
	Path    string `env:"PATH"`
}

// Validate cross-subsystem invariants. Per-subsystem validation
// (e.g. db.Config.User required) is done at Connect time.
func (c Config) Validate() error {
	if c.Auth.PrivateKeyPEM != "" && c.DB.User == "" {
		return xerrs.Validation(CodeAuthNeedsDB,
			"service: Auth.PrivateKeyPEM requires DB (refreshpg store needs a Querier)")
	}
	natsmapOn := c.NATSMap.Enabled || c.NATSMap.SubscribersPath != "" || c.NATSMap.PublishersPath != ""
	if natsmapOn && c.NATS.URL == "" {
		return xerrs.Validation(CodeNATSMapNeedsNATS,
			"service: NATSMap requires NATS (subscribers + publishers need a connection)")
	}
	return nil
}
