// Package config defines urlshort-api's environment configuration.
// Embeds gokit/service.Config so kit-level concerns (DB, Auth,
// Redis, server addr, logging) inherit their env conventions
// automatically.
//
// NOTE: api does NOT consume NATS directly. The publisher gateway
// (urlshort-publisher) republishes events onto NATS on api's
// behalf. PublisherURL points the api at that gateway.
package config

import (
	"github.com/caarlos0/env/v11"

	xerrs "github.com/theizzatbek/gokit/errs"
	"github.com/theizzatbek/gokit/service"
)

// Config is the env-driven api configuration.
type Config struct {
	service.Config

	// ShortURLBase is prepended to generated short-codes in
	// responses (e.g. http://localhost:3000/Ab1cD). Redirect uses
	// the path-suffix only — base appears in JSON shapes the API
	// returns to clients.
	ShortURLBase string `env:"SHORT_URL_BASE" envDefault:"http://localhost:3000"`

	// PublisherURL is the urlshort-publisher base URL — the api
	// POSTs LinkVisited JSON there for republish onto NATS. Empty
	// disables (api stops sending visit events — useful in local
	// dev when running api without the publisher).
	PublisherURL string `env:"PUBLISHER_URL" envDefault:"http://localhost:3001"`

	// Redis cache is auto-enabled when service.Config.Redis.URL is
	// set (env: REDIS_URL). See service/config.go::RedisConfig.
}

// Load reads Config from env, applies defaults, validates.
func Load() (Config, error) {
	var c Config
	if err := env.Parse(&c); err != nil {
		return Config{}, xerrs.Wrap(err, xerrs.KindValidation,
			"urlshort_env_parse_failed", "urlshort-api: env parse failed")
	}
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

// Validate checks urlshort-api-specific required fields.
// service.Config's own Validate is invoked by service.New.
func (c Config) Validate() error {
	if c.ShortURLBase == "" {
		return xerrs.Validation("urlshort_missing_short_url_base",
			"urlshort-api: SHORT_URL_BASE is required")
	}
	return nil
}
