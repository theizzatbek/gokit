// Package config defines the urlshort service configuration loaded from
// the environment. Embeds gokit/db.Config under envPrefix:"DB_" so the
// kit's own env conventions stay consistent ("env compose" pattern from
// the kit overview spec).
package config

import (
	"github.com/caarlos0/env/v11"
	"github.com/theizzatbek/gokit/db"
	xerrs "github.com/theizzatbek/gokit/errs"
)

// Config is the env-driven service configuration.
type Config struct {
	DB               db.Config `envPrefix:"DB_"`
	NATSURL          string    `env:"NATS_URL"`
	MicrolinkBaseURL string    `env:"MICROLINK_BASE_URL"`
	JWTPrivateKeyPEM string    `env:"JWT_PRIVATE_KEY_PEM"`
	ShortURLBase     string    `env:"SHORT_URL_BASE"`
	Addr             string    `env:"ADDR"`
	LogLevel         string    `env:"LOG_LEVEL"`
}

// Load reads Config from the environment and applies defaults. Returns
// *errs.Error on any failure.
func Load() (Config, error) {
	var c Config
	if err := env.Parse(&c); err != nil {
		return Config{}, xerrs.Wrap(err, xerrs.KindValidation,
			"urlshort_env_parse_failed", "urlshort: env parse failed")
	}
	c.ApplyDefaults()
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

// Validate enforces presence of all required custom fields. The
// embedded db.Config validates itself via env-tag `required` markers
// during env.Parse.
func (c Config) Validate() error {
	if c.NATSURL == "" {
		return xerrs.Validation("urlshort_missing_nats_url", "urlshort: NATS_URL is required")
	}
	if c.MicrolinkBaseURL == "" {
		return xerrs.Validation("urlshort_missing_microlink_base_url",
			"urlshort: MICROLINK_BASE_URL is required")
	}
	if c.JWTPrivateKeyPEM == "" {
		return xerrs.Validation("urlshort_missing_jwt_private_key",
			"urlshort: JWT_PRIVATE_KEY_PEM is required (Ed25519 PEM)")
	}
	if c.ShortURLBase == "" {
		return xerrs.Validation("urlshort_missing_short_url_base",
			"urlshort: SHORT_URL_BASE is required")
	}
	return nil
}

// ApplyDefaults fills server/log defaults.
func (c *Config) ApplyDefaults() {
	if c.Addr == "" {
		c.Addr = ":3000"
	}
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
}
