package natsclient

import (
	"os"
	"path/filepath"
	"time"

	xerrs "github.com/theizzatbek/gokit/errs"
)

// Config is the required configuration for Connect. Tunables that don't change
// connection identity live on Option (Logger, Metrics, Codec, reconnect handlers).
type Config struct {
	URL     string        // nats://host:port — comma-separated for cluster
	Name    string        // client name visible in NATS monitoring; default = filepath.Base(os.Args[0])
	Timeout time.Duration // connect timeout; default 5s

	// Auth — pick at most ONE method. None set = anonymous.
	Token     string // simple token auth
	User      string // basic user/pass — requires both User and Password
	Password  string
	CredsFile string // NATS 2.0+ JWT creds file
	NKeySeed  string // raw NKey seed

	// Reconnect — sensible defaults; override only if needed.
	MaxReconnects int           // -1 = infinite (default)
	ReconnectWait time.Duration // 2s (default)
}

// validate checks the Config for misuse. Returns *errs.Error on failure.
func (c Config) validate() error {
	if c.URL == "" {
		return xerrs.Validation(CodeMissingURL, "natsclient.Config.URL is required")
	}
	// "basic" = User+Password as one method.
	basic := c.User != "" || c.Password != ""
	if basic && (c.User == "" || c.Password == "") {
		return xerrs.Validation(CodeAuthAmbiguous, "natsclient.Config: User and Password must be set together")
	}
	methods := 0
	if c.Token != "" {
		methods++
	}
	if basic {
		methods++
	}
	if c.CredsFile != "" {
		methods++
	}
	if c.NKeySeed != "" {
		methods++
	}
	if methods > 1 {
		return xerrs.Validation(CodeAuthAmbiguous, "natsclient.Config: multiple auth methods set; choose exactly one")
	}
	return nil
}

// applyDefaults fills in defaults for empty fields. Called after validate.
func (c *Config) applyDefaults() {
	if c.Timeout == 0 {
		c.Timeout = 5 * time.Second
	}
	if c.Name == "" {
		c.Name = filepath.Base(os.Args[0])
	}
	if c.MaxReconnects == 0 {
		c.MaxReconnects = -1
	}
	if c.ReconnectWait == 0 {
		c.ReconnectWait = 2 * time.Second
	}
}
