package svckit

import (
	"time"

	"github.com/theizzatbek/gokit/clients/httpc"
	"github.com/theizzatbek/gokit/db"
	xerrs "github.com/theizzatbek/gokit/errs"
)

// Default YAML file paths used by svckit subsystems when Enabled is set
// but no explicit Path override is supplied.
const (
	DefaultRoutesPath = "routes.yaml"
)

// Config is the env-driven core configuration. Compose into your own
// app config via embedding to add app-specific fields:
//
//	type MyConfig struct {
//	    svckit.Config
//	    MyField string `env:"MY_FIELD"`
//	}
type Config struct {
	Service ServiceConfig `envPrefix:""`
	DB      db.Config     `envPrefix:"DB_"`
	Auth    AuthConfig    `envPrefix:"AUTH_"`
	HTTPC   httpc.Config  `envPrefix:"HTTPC_"`
	Routes  RoutesConfig  `envPrefix:"ROUTES_"`
}

// ServiceConfig — server + logging knobs.
//
// NodeName identifies the running instance in multi-node deployments;
// it defaults to os.Hostname() when unset, flows to slog default
// attrs as "node", and is exposed to mods via Host.NodeName(). Mods
// that need per-node identity (e.g. a NATS connection name) read it
// from there instead of the core knowing about them. ServerGroup
// labels a cluster of nodes that share work; it flows to slog default
// attrs as "server_group" and is exposed to mods via
// Host.ServerGroup() for their own grouping needs (e.g. queue-group
// suffixes).
type ServiceConfig struct {
	Addr        string `env:"ADDR"         envDefault:":3000"`
	LogLevel    string `env:"LOG_LEVEL"    envDefault:"info"`
	LogFormat   string `env:"LOG_FORMAT"   envDefault:"json"` // json | text
	NodeName    string `env:"NODE_NAME"`
	ServerGroup string `env:"SERVER_GROUP"`

	// ConfigsDir is the directory the kit looks in for every
	// default-named YAML (routes.yaml, …). Empty (default) preserves
	// the current CWD-based lookup. When set, e.g. "configs", the kit
	// reads configs/routes.yaml.
	//
	// Per-subsystem `Path` overrides (Routes.Path, …) are taken as
	// operator-supplied literal paths and are NOT prefixed — keeping
	// the override channel transparent.
	ConfigsDir string `env:"CONFIGS_DIR"`

	// Env labels the runtime environment ("dev", "staging",
	// "prod"). [WithDevMode] uses this as the gate for mounting
	// dev-only inspectors — non-"dev" values disable mounting
	// even when the option was passed.
	Env string `env:"ENV"`

	// CORSOrigins, when non-empty AND no [WithCORS] / [WithCORSConfig]
	// option was passed by the caller, triggers env-driven CORS
	// auto-enable inside [New]. Comma-separated list of allowed
	// origins; same shape WithCORS takes as its variadic. Caller-
	// supplied WithCORS always wins.
	//
	// AllowCredentials matches the WithCORS contract: enabled when
	// every entry is explicit; auto-disabled when "*" appears (CORS
	// spec rejects `Access-Control-Allow-Origin: *` with credentials).
	//
	// For full control (custom headers, MaxAge, AllowOriginsFunc),
	// use [WithCORSConfig] explicitly — env auto-enable only covers
	// the kit-defaulted shape.
	CORSOrigins string `env:"CORS_ORIGINS"` // csv

	// TLSCertFile / TLSKeyFile, when both non-empty, make [Service.Run]
	// serve HTTPS via fibermap.WithTLS (app.ListenTLS). For edge
	// deployments without an ingress terminating TLS in front of the
	// service, and for local Secure-cookie flows. The pair is
	// all-or-nothing: exactly one set fails Config.Validate with
	// *errs.Error{Code: CodeTLSConfigIncomplete} instead of silently
	// starting plain HTTP. Caller-supplied [WithTLS] wins over these.
	TLSCertFile string `env:"TLS_CERT_FILE"`
	TLSKeyFile  string `env:"TLS_KEY_FILE"`
}

// AuthConfig — JWT signing material + TTLs. PrivateKeyPEM is the
// opt-in trigger; empty means "no auth in this service".
type AuthConfig struct {
	PrivateKeyPEM string        `env:"PRIVATE_KEY_PEM"`
	KID           string        `env:"KID"         envDefault:"k1"`
	Issuer        string        `env:"ISSUER"      envDefault:"gokit"`
	AccessTTL     time.Duration `env:"ACCESS_TTL"  envDefault:"15m"`
	RefreshTTL    time.Duration `env:"REFRESH_TTL" envDefault:"720h"` // 30d

	// APIKeyHashSecret is the HMAC pepper auth.APIKey middleware
	// uses to derive `keyHash` from a plain key before calling
	// KeyStore.Lookup. Required only when the service wires API-key
	// auth (auth/fibermount.MountAPIKeyFactory or a manual
	// auth.APIKey middleware install); safe to omit for pure-JWT
	// services.
	//
	// Encoding: standard or URL-safe base64 (padded or raw — every
	// flavour accepted). Decoded bytes MUST be ≥ 32 bytes (HMAC-
	// SHA256 best practice). Core setup fails with
	// *errs.Error{Code: CodeAuthInvalidAPIKeyHashSecret} when the
	// env / config string fails to decode or the decoded length is
	// short.
	//
	// Threaded through to auth.New via auth.WithAPIKeyHashSecret;
	// callers building auth.Auth[C] by hand can use that Option
	// directly with raw decoded bytes.
	APIKeyHashSecret string `env:"APIKEY_HASH_SECRET"`
}

// RoutesConfig — Enabled (or Path override) triggers routes auto-load
// in svc.Run, after user-side RegisterHandler calls and before
// engine.Mount.
type RoutesConfig struct {
	Enabled bool   `env:"ENABLED"`
	Path    string `env:"PATH"`
}

// Validate checks what the core can check before Build. Cross-
// subsystem checks moved to the mods along with the subsystems: "natsmap
// requires NATS" no longer exists as an error class, because both now
// live in the same mod.
func (c Config) Validate() error {
	if c.Auth.PrivateKeyPEM != "" && c.DB.User == "" {
		return xerrs.Validation(CodeAuthNeedsDB,
			"svckit: Auth.PrivateKeyPEM requires DB (refreshpg store needs a Querier)")
	}
	if (c.Service.TLSCertFile == "") != (c.Service.TLSKeyFile == "") {
		return xerrs.Validation(CodeTLSConfigIncomplete,
			"svckit: TLS_CERT_FILE and TLS_KEY_FILE must be set together (got only one); refusing to fall back to plain HTTP")
	}
	return nil
}
