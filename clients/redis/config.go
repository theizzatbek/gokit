package redisclient

import (
	"time"

	xerrs "github.com/theizzatbek/gokit/errs"
)

// Config is the required configuration for Connect (single-node mode).
// Tunables that don't change connection identity (Logger, Metrics,
// custom redis options) live on Option.
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

// ClusterConfig is the cluster-mode configuration consumed by
// [ConnectCluster]. Mirrors the connect-retry shape of [Config].
type ClusterConfig struct {
	// Addrs is the list of seed cluster node addresses
	// (`host:port`). Required (length >= 1).
	Addrs []string

	// Username / Password optional ACL credentials applied to every
	// shard.
	Username string
	Password string

	// ConnectMaxRetries / ConnectBackoffBase / ConnectBackoffMax —
	// same semantics as [Config].
	ConnectMaxRetries  int
	ConnectBackoffBase time.Duration
	ConnectBackoffMax  time.Duration
}

func (c ClusterConfig) validate() error {
	if len(c.Addrs) == 0 {
		return xerrs.Validation(CodeMissingURL, "redisclient.ClusterConfig.Addrs is required")
	}
	return nil
}

// SentinelConfig is the Redis Sentinel failover configuration
// consumed by [ConnectSentinel]. Use when the deployment fronts
// Redis with a Sentinel cluster for HA.
type SentinelConfig struct {
	// MasterName is the Sentinel "master name" identifier (set by
	// the Sentinel operator). Required.
	MasterName string

	// SentinelAddrs is the list of Sentinel node addresses
	// (`host:port`). Required.
	SentinelAddrs []string

	// DB / Username / Password optional credentials applied to the
	// resolved master and replicas.
	DB       int
	Username string
	Password string

	// SentinelUsername / SentinelPassword optional ACL credentials
	// for the Sentinel layer itself (separate from the data-plane
	// creds above).
	SentinelUsername string
	SentinelPassword string

	ConnectMaxRetries  int
	ConnectBackoffBase time.Duration
	ConnectBackoffMax  time.Duration
}

func (c SentinelConfig) validate() error {
	if c.MasterName == "" {
		return xerrs.Validation(CodeMissingURL, "redisclient.SentinelConfig.MasterName is required")
	}
	if len(c.SentinelAddrs) == 0 {
		return xerrs.Validation(CodeMissingURL, "redisclient.SentinelConfig.SentinelAddrs is required")
	}
	return nil
}
