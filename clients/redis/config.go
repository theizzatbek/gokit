package redisclient

import (
	"time"

	xerrs "github.com/theizzatbek/gokit/errs"
)

// Config is the required configuration for Connect. Tunables that
// don't change connection identity (Logger, Metrics, custom redis
// options) live on Option.
type Config struct {
	// URL is the standard redis:// connection string. Required.
	// Examples:
	//
	//	redis://localhost:6379
	//	redis://user:password@host:6379/2
	//	rediss://host:6380/0           — TLS
	URL string

	// ConnectMaxRetries caps the number of retry attempts during the
	// initial Connect ping. 0 = no retry (1 attempt). N>0 = N
	// additional attempts. service.New auto-defaults to 5 when this
	// is 0; pass -1 via env to disable that injection.
	ConnectMaxRetries int

	// ConnectBackoffBase doubles each attempt, capped at
	// ConnectBackoffMax. Default 0 (service injects 1s).
	ConnectBackoffBase time.Duration

	// ConnectBackoffMax caps the per-attempt wait. Default 0 (service
	// injects 16s).
	ConnectBackoffMax time.Duration
}

// validate checks the Config for misuse. Returns *errs.Error on failure.
func (c Config) validate() error {
	if c.URL == "" {
		return xerrs.Validation(CodeMissingURL, "redisclient.Config.URL is required")
	}
	return nil
}
