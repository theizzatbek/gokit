package testdb

import "time"

// Option tunes [Spin] (and [SpinCluster] for the shared bits — image,
// credentials, startup timeout).
type Option func(*config)

// ClusterOption tunes [SpinCluster] only. ClusterOption extends Option
// so the same image / credentials / timeout setters work for both.
type ClusterOption func(*config)

// config holds the merged Option/ClusterOption settings before the
// helper builds containers.
type config struct {
	image           string // postgres image, single-node mode
	clusterImage    string // postgres image, cluster mode (must support streaming replication via env vars)
	database        string
	username        string
	password        string
	replicaUser     string // replication-account user (cluster only)
	replicaPassword string // replication-account password (cluster only)
	startupTimeout  time.Duration
	maxConns        int32
	configKVs       map[string]string // extra postgresql.conf keys (cluster primary)
	freshPerTest    bool
}

// defaultConfig populates the kit-chosen baselines. Caller options
// over-write the matching fields.
func defaultConfig() config {
	return config{
		image:           "postgres:16-alpine",
		clusterImage:    "bitnami/postgresql:16",
		database:        "testdb",
		username:        "test",
		password:        "test",
		replicaUser:     "repl",
		replicaPassword: "repl",
		startupTimeout:  120 * time.Second,
		maxConns:        4,
	}
}

// WithImage overrides the Postgres image used by [Spin]. Default
// "postgres:16-alpine". Pass a pinned tag in CI to dodge silent
// major-version drift.
func WithImage(image string) Option {
	return func(c *config) { c.image = image }
}

// WithClusterImage overrides the Postgres image used by
// [SpinCluster]. Default "bitnami/postgresql:16" — the image MUST
// support the POSTGRESQL_REPLICATION_MODE env vars Bitnami ships
// with; the official `postgres:` image does NOT.
func WithClusterImage(image string) ClusterOption {
	return func(c *config) { c.clusterImage = image }
}

// WithDatabase overrides the database name created in the container.
// Default "testdb".
func WithDatabase(name string) Option {
	return func(c *config) { c.database = name }
}

// WithCredentials overrides the application user/password. Default
// "test"/"test". The kit never persists these — they live only inside
// the container.
func WithCredentials(user, password string) Option {
	return func(c *config) {
		c.username = user
		c.password = password
	}
}

// WithReplicationCredentials overrides the replication-account
// user/password used by Bitnami's standby containers to stream from
// the primary. Default "repl"/"repl". Cluster-only.
func WithReplicationCredentials(user, password string) ClusterOption {
	return func(c *config) {
		c.replicaUser = user
		c.replicaPassword = password
	}
}

// WithStartupTimeout caps how long the helper waits for each
// container to report "ready to accept connections". Default 120s —
// generous to absorb slow Docker-pull on a cold CI cache; lower to
// fail fast in local-dev.
func WithStartupTimeout(d time.Duration) Option {
	return func(c *config) { c.startupTimeout = d }
}

// WithMaxConns caps the per-pool MaxConns. Default 4 — sufficient
// for most integration tests; raise when the test exercises
// concurrency.
func WithMaxConns(n int32) Option {
	return func(c *config) {
		if n > 0 {
			c.maxConns = n
		}
	}
}

// WithConfigKVs injects extra postgresql.conf key/value pairs into
// the primary container at startup. Cluster-only — the single-node
// helper uses the upstream image defaults.
//
//	testdb.WithConfigKVs(map[string]string{
//	    "max_wal_senders": "20",
//	    "wal_keep_size":   "128MB",
//	})
func WithConfigKVs(kv map[string]string) ClusterOption {
	return func(c *config) {
		if c.configKVs == nil {
			c.configKVs = map[string]string{}
		}
		for k, v := range kv {
			c.configKVs[k] = v
		}
	}
}

// WithFreshPerTest disables container reuse — each [Spin] call
// builds a brand-new container. Default behaviour reuses one
// container per test binary and isolates tests via fresh schemas.
//
// Use only when:
//   - The test mutates global server state (settings, roles) that
//     SET search_path can't shadow.
//   - You hit a confirmed cross-test interaction that schema
//     isolation should have caught but didn't.
//
// SpinCluster is ALWAYS fresh-per-call (replicas don't share state
// cleanly across tests). This option does nothing in cluster mode.
func WithFreshPerTest() Option {
	return func(c *config) { c.freshPerTest = true }
}

// applyAll merges the supplied Option-or-ClusterOption slice over
// the default config. Returns the resulting config so the caller
// can read it.
func applyOptions(opts []Option) config {
	c := defaultConfig()
	for _, fn := range opts {
		fn(&c)
	}
	return c
}

// applyClusterOptions merges ClusterOption (which is the same shape
// as Option). Done as a separate function to keep the Spin-only and
// SpinCluster-only call sites visually distinct.
func applyClusterOptions(opts []ClusterOption) config {
	c := defaultConfig()
	for _, fn := range opts {
		fn(&c)
	}
	return c
}
