// Package config defines the urlshort service configuration loaded from
// the environment. Embeds gokit/service.Config so kit-level concerns
// (DB, Auth, NATS, HTTPC, APIMap, server addr, logging) inherit their
// env conventions automatically — only urlshort-specific fields
// (Microlink base URL, short-URL base) live here directly.
package config

import (
	"github.com/caarlos0/env/v11"
	xerrs "github.com/theizzatbek/gokit/errs"
	"github.com/theizzatbek/gokit/service"
)

// Config is the env-driven urlshort configuration.
type Config struct {
	service.Config

	MicrolinkBaseURL string `env:"MICROLINK_BASE_URL"`
	ShortURLBase     string `env:"SHORT_URL_BASE" envDefault:"http://localhost:3000"`

	// Redis-backed cache is enabled when service.Config.Redis.URL is
	// set (env: REDIS_URL). See service/config.go::RedisConfig.
}

// Load reads Config from env, applies defaults, validates.
func Load() (Config, error) {
	var c Config
	if err := env.Parse(&c); err != nil {
		return Config{}, xerrs.Wrap(err, xerrs.KindValidation,
			"urlshort_env_parse_failed", "urlshort: env parse failed")
	}
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

// Validate checks urlshort-specific required fields. service.Config's
// own Validate is invoked by service.New.
func (c Config) Validate() error {
	if c.MicrolinkBaseURL == "" {
		return xerrs.Validation("urlshort_missing_microlink_base_url",
			"urlshort: MICROLINK_BASE_URL is required")
	}
	if c.ShortURLBase == "" {
		return xerrs.Validation("urlshort_missing_short_url_base",
			"urlshort: SHORT_URL_BASE is required")
	}
	return nil
}
