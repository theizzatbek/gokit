// Package config defines the environment-driven configuration for the
// tasks example. One flat struct, parsed via caarlos0/env/v11. No
// sub-packages, no layering — this is an example, not a service.
package config

import (
	"fmt"
	"time"

	"github.com/caarlos0/env/v11"
)

// Config is the full set of runtime knobs. Every field has an
// envDefault so a fresh checkout runs without any .env file. See
// .env.example for documentation per field.
type Config struct {
	// Server. Addr has no envDefault on purpose: when empty,
	// fibermap.Run honors $PORT (cloud-platform convention) and falls
	// back to :3000. Setting ADDR explicitly always wins.
	Addr            string        `env:"ADDR"`
	ShutdownTimeout time.Duration `env:"SHUTDOWN_TIMEOUT" envDefault:"10s"`
	BodyLimit       int           `env:"BODY_LIMIT"       envDefault:"1048576"` // 1 MiB

	// Logging
	LogLevel  string `env:"LOG_LEVEL"  envDefault:"info"` // debug|info|warn|error
	LogFormat string `env:"LOG_FORMAT" envDefault:"json"` // json|text

	// CORS
	CORSOrigins []string `env:"CORS_ORIGINS" envSeparator:"," envDefault:"*"`
	CORSMethods []string `env:"CORS_METHODS" envSeparator:"," envDefault:"GET,POST,PATCH,DELETE,OPTIONS"`

	// Rate limit (per-IP via fiber/limiter)
	RateLimitMax        int           `env:"RATE_LIMIT_MAX"        envDefault:"100"`
	RateLimitExpiration time.Duration `env:"RATE_LIMIT_EXPIRATION" envDefault:"1m"`

	// App
	Env        string `env:"ENV"           envDefault:"development"` // development|staging|production
	APIBaseURL string `env:"API_BASE_URL"`                           // optional → OpenAPI server URL
}

// Load reads Config from the process environment and validates it.
// Returns the zero Config and an error on the first problem.
func Load() (Config, error) {
	cfg, err := env.ParseAs[Config]()
	if err != nil {
		return Config{}, fmt.Errorf("config: parse env: %w", err)
	}
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) validate() error {
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("config: LOG_LEVEL=%q (want debug|info|warn|error)", c.LogLevel)
	}
	switch c.LogFormat {
	case "json", "text":
	default:
		return fmt.Errorf("config: LOG_FORMAT=%q (want json|text)", c.LogFormat)
	}
	switch c.Env {
	case "development", "staging", "production":
	default:
		return fmt.Errorf("config: ENV=%q (want development|staging|production)", c.Env)
	}
	if c.BodyLimit <= 0 {
		return fmt.Errorf("config: BODY_LIMIT=%d (must be > 0)", c.BodyLimit)
	}
	if c.RateLimitMax <= 0 {
		return fmt.Errorf("config: RATE_LIMIT_MAX=%d (must be > 0)", c.RateLimitMax)
	}
	return nil
}
