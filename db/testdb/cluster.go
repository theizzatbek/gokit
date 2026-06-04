package testdb

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/theizzatbek/gokit/db"
)

// Cluster is the [SpinCluster] return value. Three handles into the
// same physical postgres cluster:
//   - Primary: dedicated *db.DB against the writable master. Use for
//     writes + read-after-write tests that demand strong consistency.
//   - Replicas: per-replica *db.DB handles. Each one connects ONLY to
//     its own standby; use to probe per-replica state (replication
//     lag, individual standby health) directly.
//   - Multi: a single *db.DB wired with [db.Config.ReadURLs] spanning
//     every Replica. ReadQuery/ReadQueryRow rotate across replicas via
//     the kit's normal routing — use to test code that consumes the
//     kit's multi-replica surface as it would in production.
//
// All four shared the cluster's lifetime; both Close + container
// Terminate run via t.Cleanup.
type Cluster struct {
	Primary  *db.DB
	Replicas []*db.DB
	Multi    *db.DB
}

// SpinCluster boots a primary + N standby Postgres cluster with
// streaming replication and returns a [Cluster]. Cleanup (Close +
// Terminate the whole network) is registered with t.Cleanup.
//
// `replicas` must be ≥ 1; `replicas == 0` is treated as 1 to keep
// the kit's multi-replica routing observable (the whole point of
// SpinCluster). Skips under `go test -short`.
//
// Uses the Bitnami postgresql image because its built-in
// POSTGRESQL_REPLICATION_MODE wiring removes the pg_basebackup
// dance the kit would otherwise have to script. Override via
// [WithClusterImage] when CI must pin a specific tag.
//
// Boot time: ~15-30s for primary + 1 replica on a warm Docker cache;
// scales roughly linearly with replica count. The startup timeout
// (default 120s, override via [WithStartupTimeout]) bounds the wait.
func SpinCluster(t *testing.T, replicas int, opts ...ClusterOption) *Cluster {
	t.Helper()
	if testing.Short() {
		t.Skip("requires Docker; rerun without -short")
	}
	if replicas <= 0 {
		replicas = 1
	}
	cfg := applyClusterOptions(opts)
	ctx, cancel := context.WithTimeout(context.Background(), cfg.startupTimeout)
	defer cancel()

	cluster, teardown, err := bootCluster(ctx, replicas, cfg)
	if err != nil {
		if teardown != nil {
			teardown()
		}
		t.Fatalf("testdb: cluster bootstrap: %v", err)
	}
	t.Cleanup(teardown)
	return cluster
}

// bootCluster does the orchestration: docker network, primary
// container, N standby containers, primary-replica handshake, *db.DB
// wires. Returns a single teardown closure that drops everything in
// reverse-creation order.
func bootCluster(ctx context.Context, replicas int, cfg config) (*Cluster, func(), error) {
	// Aggregate every successful container + db.DB so the teardown
	// closure can reverse them even on a mid-boot failure.
	var (
		containers []testcontainers.Container
		handles    []*db.DB
		net        *testcontainers.DockerNetwork
	)
	teardown := func() {
		for _, h := range handles {
			h.Close()
		}
		for i := len(containers) - 1; i >= 0; i-- {
			_ = testcontainers.TerminateContainer(containers[i])
		}
		if net != nil {
			_ = net.Remove(context.Background())
		}
	}

	n, err := network.New(ctx)
	if err != nil {
		return nil, teardown, fmt.Errorf("network: %w", err)
	}
	net = n

	primary, primaryHost, primaryPort, err := startBitnamiPrimary(ctx, cfg, net.Name)
	if err != nil {
		return nil, teardown, fmt.Errorf("primary: %w", err)
	}
	containers = append(containers, primary)

	primaryDB, err := db.Connect(context.Background(), db.Config{
		Host: primaryHost, Port: primaryPort,
		User: cfg.username, Password: cfg.password, Database: cfg.database,
		SSLMode: "disable", ConnectTimeout: 5 * time.Second,
		MaxConns: cfg.maxConns,
	})
	if err != nil {
		return nil, teardown, fmt.Errorf("connect primary: %w", err)
	}
	handles = append(handles, primaryDB)

	// Boot each standby. Bitnami's image bootstraps from the primary
	// over the docker network using the network alias "primary".
	replicaURLs := make([]string, 0, replicas)
	replicaDBs := make([]*db.DB, 0, replicas)
	for i := 0; i < replicas; i++ {
		c, host, port, rerr := startBitnamiReplica(ctx, cfg, net.Name, i+1)
		if rerr != nil {
			return nil, teardown, fmt.Errorf("replica %d: %w", i+1, rerr)
		}
		containers = append(containers, c)
		rdb, rerr := db.Connect(context.Background(), db.Config{
			Host: host, Port: port,
			User: cfg.username, Password: cfg.password, Database: cfg.database,
			SSLMode: "disable", ConnectTimeout: 5 * time.Second,
			MaxConns: cfg.maxConns,
		})
		if rerr != nil {
			return nil, teardown, fmt.Errorf("connect replica %d: %w", i+1, rerr)
		}
		handles = append(handles, rdb)
		replicaDBs = append(replicaDBs, rdb)
		replicaURLs = append(replicaURLs,
			fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable",
				cfg.username, cfg.password, host, port, cfg.database))
	}

	// Multi-DB wires the primary as the writable pool + every replica
	// in ReadURLs, mirroring what production code consuming the kit
	// would set via DB_READ_URLS.
	multi, err := db.Connect(context.Background(), db.Config{
		Host: primaryHost, Port: primaryPort,
		User: cfg.username, Password: cfg.password, Database: cfg.database,
		SSLMode: "disable", ConnectTimeout: 5 * time.Second,
		MaxConns:     cfg.maxConns,
		ReadURLs:     replicaURLs,
		ReadStrategy: "round_robin",
	})
	if err != nil {
		return nil, teardown, fmt.Errorf("multi DB: %w", err)
	}
	handles = append(handles, multi)

	return &Cluster{
		Primary:  primaryDB,
		Replicas: replicaDBs,
		Multi:    multi,
	}, teardown, nil
}

// startBitnamiPrimary launches the master container on the supplied
// docker network with the alias "primary" so standbys can resolve
// each other by name regardless of the random container hostname
// testcontainers picks. Returns the container plus the host:port
// pair the test process can reach from outside the network.
func startBitnamiPrimary(ctx context.Context, cfg config, networkName string) (testcontainers.Container, string, int, error) {
	env := map[string]string{
		"POSTGRESQL_REPLICATION_MODE":     "master",
		"POSTGRESQL_REPLICATION_USER":     cfg.replicaUser,
		"POSTGRESQL_REPLICATION_PASSWORD": cfg.replicaPassword,
		"POSTGRESQL_USERNAME":             cfg.username,
		"POSTGRESQL_PASSWORD":             cfg.password,
		"POSTGRESQL_DATABASE":             cfg.database,
		// Setting the postgres-superuser password lets standbys
		// connect for the replication handshake AND lets the kit run
		// the pg_last_xact_replay_timestamp() probe against either
		// node without auth surprises.
		"POSTGRESQL_POSTGRES_PASSWORD": cfg.password,
	}
	for k, v := range cfg.configKVs {
		// Bitnami honours postgresql.conf overrides via
		// POSTGRESQL_PG_CONF_<KEY>=value (and via mounted config),
		// but the simpler escape is to use POSTGRESQL_EXTRA_FLAGS
		// for command-line -c args. We pick the command-line form so
		// the helper stays single-file.
		env["POSTGRESQL_EXTRA_FLAGS"] += " -c " + k + "=" + v
	}
	req := testcontainers.ContainerRequest{
		Image:        cfg.clusterImage,
		ExposedPorts: []string{"5432/tcp"},
		Env:          env,
		Networks:     []string{networkName},
		NetworkAliases: map[string][]string{
			networkName: {"primary"},
		},
		WaitingFor: wait.ForLog("database system is ready to accept connections").
			WithStartupTimeout(cfg.startupTimeout),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return nil, "", 0, err
	}
	host, err := c.Host(ctx)
	if err != nil {
		return c, "", 0, err
	}
	port, err := c.MappedPort(ctx, "5432/tcp")
	if err != nil {
		return c, "", 0, err
	}
	p, _ := strconv.Atoi(port.Port())
	return c, host, p, nil
}

// startBitnamiReplica launches one standby container that streams
// from the primary's network alias "primary". `index` is a 1-based
// counter used only for the network alias ("standby-1", …).
func startBitnamiReplica(ctx context.Context, cfg config, networkName string, index int) (testcontainers.Container, string, int, error) {
	env := map[string]string{
		"POSTGRESQL_REPLICATION_MODE":     "slave",
		"POSTGRESQL_REPLICATION_USER":     cfg.replicaUser,
		"POSTGRESQL_REPLICATION_PASSWORD": cfg.replicaPassword,
		"POSTGRESQL_USERNAME":             cfg.username,
		"POSTGRESQL_PASSWORD":             cfg.password,
		"POSTGRESQL_DATABASE":             cfg.database,
		"POSTGRESQL_POSTGRES_PASSWORD":    cfg.password,
		"POSTGRESQL_MASTER_HOST":          "primary",
		"POSTGRESQL_MASTER_PORT_NUMBER":   "5432",
	}
	alias := fmt.Sprintf("standby-%d", index)
	req := testcontainers.ContainerRequest{
		Image:        cfg.clusterImage,
		ExposedPorts: []string{"5432/tcp"},
		Env:          env,
		Networks:     []string{networkName},
		NetworkAliases: map[string][]string{
			networkName: {alias},
		},
		WaitingFor: wait.ForLog("database system is ready to accept read-only connections").
			WithStartupTimeout(cfg.startupTimeout),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return nil, "", 0, err
	}
	host, err := c.Host(ctx)
	if err != nil {
		return c, "", 0, err
	}
	port, err := c.MappedPort(ctx, "5432/tcp")
	if err != nil {
		return c, "", 0, err
	}
	p, _ := strconv.Atoi(port.Port())
	return c, host, p, nil
}

// WaitForReplication blocks until the cluster's primary LSN matches
// every replica's replay LSN, or ctx expires. Use immediately after
// a write transaction in tests that subsequently read from a replica
// to remove the cross-network races.
//
// Returns ctx.Err() on timeout; nil when every replica caught up.
// A no-replica cluster (defensive — SpinCluster always has ≥1)
// returns nil immediately.
func (c *Cluster) WaitForReplication(ctx context.Context) error {
	if c == nil || c.Primary == nil || len(c.Replicas) == 0 {
		return nil
	}
	const (
		pollEvery = 50 * time.Millisecond
		query     = `
			SELECT
			  (SELECT pg_current_wal_lsn()) - (SELECT pg_last_wal_replay_lsn()) AS lag_bytes
		`
	)
	ticker := time.NewTicker(pollEvery)
	defer ticker.Stop()
	for {
		caughtUp := true
		for _, r := range c.Replicas {
			var lag int64
			if err := r.QueryRow(ctx, query).Scan(&lag); err != nil {
				return fmt.Errorf("replica lag probe: %w", err)
			}
			if lag != 0 {
				caughtUp = false
				break
			}
		}
		if caughtUp {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
