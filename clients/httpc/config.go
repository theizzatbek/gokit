package httpc

import (
	"time"

	xerrs "github.com/theizzatbek/gokit/errs"
)

// Config controls the retry transport's behaviour. All fields are optional;
// applyDefaults fills zero values with sensible production defaults.
type Config struct {
	Timeout     time.Duration // per-attempt; default 10s
	MaxRetries  int           // default 3 (zero value gets the default); pass -1 to disable retries entirely
	BackoffBase time.Duration // initial exponential delay; default 100ms
	BackoffMax  time.Duration // cap; default 5s
}

func (c Config) validate() error {
	if c.Timeout < 0 {
		return xerrs.Validation(CodeInvalidTimeout, "httpc.Config.Timeout must be >= 0")
	}
	if c.MaxRetries < -1 {
		return xerrs.Validation(CodeInvalidMaxRetries, "httpc.Config.MaxRetries must be >= -1 (use -1 to disable retries)")
	}
	if c.BackoffBase < 0 {
		return xerrs.Validation(CodeInvalidBackoff, "httpc.Config.BackoffBase must be >= 0")
	}
	if c.BackoffMax < 0 {
		return xerrs.Validation(CodeInvalidBackoff, "httpc.Config.BackoffMax must be >= 0")
	}
	// Only check ordering if both are non-zero (zero means "apply default").
	if c.BackoffBase > 0 && c.BackoffMax > 0 && c.BackoffMax < c.BackoffBase {
		return xerrs.Validation(CodeInvalidBackoff, "httpc.Config.BackoffMax must be >= BackoffBase")
	}
	return nil
}

func (c *Config) applyDefaults() {
	if c.Timeout == 0 {
		c.Timeout = 10 * time.Second
	}
	switch {
	case c.MaxRetries == 0:
		c.MaxRetries = 3
	case c.MaxRetries < 0:
		// -1 sentinel: caller explicitly wants no retries. Map to 0 for the loop.
		c.MaxRetries = 0
	}
	if c.BackoffBase == 0 {
		c.BackoffBase = 100 * time.Millisecond
	}
	if c.BackoffMax == 0 {
		c.BackoffMax = 5 * time.Second
	}
}
