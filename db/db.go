package db

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/theizzatbek/gokit/errs"
)

// Querier is implemented by both *DB and *Tx so repository functions can be
// written once and called against either.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// readPoolEntry pairs a pgxpool with a stable display name used for metric
// labels, log fields, and ReplicaLagInfo.PoolName. When the kit derives the
// slice from Config.HasReadReplica, the single entry is named "standby"
// (back-compat with the previous single-pool surface). For
// Config.ReadURLs, entries are named "standby-1" / "standby-2" / … in
// list order.
//
// The atomic state (lagMillis + healthy) is mutated by the lag-polling
// goroutine and read on the hot path by [pickReadPool]. healthy starts
// at 1 (the kit assumes a freshly-connected replica is good until
// proven otherwise); lagMillis starts at 0.
type readPoolEntry struct {
	name string
	pool *pgxpool.Pool

	// lagMillis is the lag in milliseconds reported by the most
	// recent [WithReplicaLagPolling] probe. Read on the hot path via
	// atomic.LoadInt64 by pickReadPool when [WithReadLagBudget] is
	// configured. -1 means "no recent probe available" (counts as
	// healthy for routing — the kit favours optimism over surprising
	// the caller).
	lagMillis atomic.Int64

	// healthy is 1 when the most recent probe succeeded, 0 when it
	// failed. pickReadPool skips entries with healthy=0; the
	// background poller revives them on the next successful probe.
	healthy atomic.Bool
}

// readRoute names the routing policy across multiple read pools.
type readRoute int

const (
	routeRoundRobin readRoute = iota
	routeRandom
)

// DB wraps a *pgxpool.Pool with the kit's error-mapping and transaction
// helpers. When Config.HasReadReplica or Config.ReadURLs were set at
// Connect time, DB also holds one or more read-replica pools exposed via
// ReadQuery / ReadQueryRow / ReadPools.
type DB struct {
	pool      *pgxpool.Pool    // primary (target_session_attrs=read-write)
	readPools []*readPoolEntry // dedicated standbys; nil when no replicas configured
	route     readRoute        // routing strategy across readPools
	nextRead  atomic.Uint64    // round-robin counter
	opts      options

	lagPoll struct {
		cancel   context.CancelFunc
		done     chan struct{}
		stopOnce sync.Once
	}
}

// readPrefKey marks the ctx as "force-primary even on Read paths". Used
// by [ReadFromPrimary] for the read-your-writes pattern (run a write tx,
// then call ReadFromPrimary(ctx) on the subsequent read so it does not
// race against an out-of-date replica).
type readPrefKey struct{}

// queryNameCtxKey is the marker [WithQueryName] writes to ctx so the
// kit's pgx tracer can read it back inside TraceQueryEnd and emit the
// `name=` label on the duration histogram.
type queryNameCtxKey struct{}

// WithQueryName returns a derived context that tags every db query
// issued under it with the supplied logical name. The kit's
// `db_query_duration_seconds` histogram gains a `name` label set to
// this value (and an empty `name=""` label for untagged queries).
//
// Cardinality safety: the value is consumed verbatim — DO NOT use
// user-controlled input. Restrict names to a small fixed set per
// service ("user_lookup", "list_orders", "outbox_drain", …) — the
// kit makes no attempt to bound cardinality, and a runaway name set
// will explode the metrics registry. Reach for [WithQueryName] only
// when slice-and-dice analytics actually requires per-query
// histograms; for the common case the unlabelled aggregate is fine.
//
// Nested WithQueryName calls — last write wins; the outer call's
// name is overwritten for any query issued under the inner ctx.
func WithQueryName(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, queryNameCtxKey{}, name)
}

// queryNameFrom reads the [WithQueryName] tag off ctx. Returns the
// empty string when no tag is set — the kit emits that as the
// label value, which Prometheus treats as a legitimate (if
// uninformative) label value.
func queryNameFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(queryNameCtxKey{}).(string)
	return v
}

// ReadFromPrimary returns a derived context that overrides the read
// router: subsequent ReadQuery / ReadQueryRow calls land on the primary
// pool instead of a standby. Use immediately after a write transaction
// when the caller MUST see its own writes without waiting for replica
// lag.
//
// A no-replica deployment (ReadQuery already falls back to the primary)
// is a deterministic no-op — the marker is harmless to apply.
func ReadFromPrimary(ctx context.Context) context.Context {
	return context.WithValue(ctx, readPrefKey{}, true)
}

// readFromPrimaryRequested reports whether the ctx carries the
// [ReadFromPrimary] marker.
func readFromPrimaryRequested(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v, _ := ctx.Value(readPrefKey{}).(bool)
	return v
}

// Connect opens a connection pool with cfg + opts. The returned *DB owns the
// underlying *pgxpool.Pool; call Close to release it. Returns *errs.Error of
// KindUnavailable if the pool fails its initial sanity ping.
//
// When cfg.ConnectMaxRetries > 0, transient pool-create / ping failures are
// retried with exponential backoff (base = cfg.ConnectBackoffBase, doubling
// each attempt, capped at cfg.ConnectBackoffMax). The loop honours ctx.Done()
// during backoff sleeps, returning KindUnavailable with the ctx error. Default
// 0 = single attempt, preserving fail-fast behaviour for kit-direct callers.
//
// When cfg.HasReadReplica is true, a second pool is opened against the same
// connection string with target_session_attrs=standby. If the standby pool
// fails to connect, the primary pool is closed and an *errs.Error of
// KindUnavailable is returned — no silent degradation. With WithMetrics, the
// db_pool_size_total gauge gains the pool="primary|standby" label so each
// pool is observable independently.
func Connect(ctx context.Context, cfg Config, opts ...Option) (*DB, error) {
	o := options{}
	// Config-derived options apply FIRST so user-supplied opts win on
	// the same field (last write wins on the Option closure).
	for _, fn := range configDerivedOptions(cfg) {
		fn(&o)
	}
	for _, fn := range opts {
		fn(&o)
	}
	if o.logger != nil || o.metrics != nil {
		if o.slowThreshold == 0 {
			o.slowThreshold = 500 * time.Millisecond
		}
	}

	primaryURL, err := buildPgxURL(cfg, "read-write")
	if err != nil {
		return nil, errs.Wrap(err, errs.KindInternal, "db_config_invalid", "could not build db url")
	}
	primary, err := connectPool(ctx, primaryURL, "primary", cfg, &o)
	if err != nil {
		return nil, err
	}

	route, err := parseReadRoute(cfg.ReadStrategy)
	if err != nil {
		primary.Close()
		return nil, err
	}

	d := &DB{pool: primary, route: route, opts: o}

	// ReadURLs takes precedence over HasReadReplica — operators that
	// flip ReadURLs on already have explicit replica endpoints and we
	// don't want to silently open an extra default-standby alongside.
	readURLs := nonEmptyStrings(cfg.ReadURLs)
	switch {
	case len(readURLs) > 0:
		for i, raw := range readURLs {
			cleanURL, perr := normalizeReadURL(raw)
			if perr != nil {
				closeReadPools(d.readPools)
				primary.Close()
				return nil, errs.Wrap(perr, errs.KindInternal, "db_config_invalid",
					fmt.Sprintf("could not parse read replica url at index %d", i))
			}
			name := fmt.Sprintf("standby-%d", i+1)
			rp, perr := connectPool(ctx, cleanURL, name, cfg, &o)
			if perr != nil {
				closeReadPools(d.readPools)
				primary.Close()
				return nil, perr
			}
			d.readPools = append(d.readPools, newReadPoolEntry(name, rp))
		}
	case cfg.HasReadReplica:
		readURL, err := buildPgxURL(cfg, "standby")
		if err != nil {
			primary.Close()
			return nil, errs.Wrap(err, errs.KindInternal, "db_config_invalid", "could not build read replica url")
		}
		rp, err := connectPool(ctx, readURL, "standby", cfg, &o)
		if err != nil {
			primary.Close()
			return nil, err
		}
		d.readPools = append(d.readPools, newReadPoolEntry("standby", rp))
	}

	// Spawn the lag-polling goroutine when both the option and at least
	// one replica are wired. Idempotent on Close.
	if o.lagPoll.interval > 0 && len(d.readPools) > 0 {
		d.startLagPolling()
	}

	return d, nil
}

// configDerivedOptions translates env-driven Config fields into kit
// Option closures, so a service that does nothing but
// `env.Parse(&cfg) + db.Connect(ctx, cfg)` still gets the multi-
// replica observability pack (lag polling + budget) when the
// matching env vars are set. The returned slice is applied BEFORE
// user-supplied opts so explicit programmatic configuration wins on
// the same field.
//
// Returns nil when no Config-derived options apply.
func configDerivedOptions(cfg Config) []Option {
	var out []Option
	if cfg.LagPollInterval > 0 {
		out = append(out, WithReplicaLagPolling(cfg.LagPollInterval, cfg.LagPollThreshold))
	}
	if cfg.LagBudget > 0 {
		out = append(out, WithReadLagBudget(cfg.LagBudget))
	}
	return out
}

// parseReadRoute maps the string form to the internal enum. Empty / "" /
// "round_robin" / "roundrobin" → routeRoundRobin (default); "random" →
// routeRandom. Anything else fails Connect with a stable Code.
func parseReadRoute(s string) (readRoute, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "round_robin", "round-robin", "roundrobin":
		return routeRoundRobin, nil
	case "random":
		return routeRandom, nil
	default:
		return 0, errs.Validation("db_config_invalid",
			"db: unknown ReadStrategy "+s+" (expected: round_robin | random)")
	}
}

// nonEmptyStrings filters empty / whitespace-only entries — useful when
// DB_READ_URLS contains a trailing comma or stray spaces from env parsing.
func nonEmptyStrings(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if v := strings.TrimSpace(s); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// normalizeReadURL is the per-replica URL preparation. Each replica gets
// target_session_attrs=standby injected when the URL omits it — operators
// can override by setting target_session_attrs=any explicitly (useful for
// analytics replicas that can serve read-only-but-may-be-promoted pools).
func normalizeReadURL(raw string) (string, error) {
	cfg := Config{URL: raw}
	return buildPgxURL(cfg, "standby")
}

// closeReadPools is the rollback helper called when one read-pool fails
// to connect after others have succeeded.
func closeReadPools(entries []*readPoolEntry) {
	for _, e := range entries {
		if e != nil && e.pool != nil {
			e.pool.Close()
		}
	}
}

// newReadPoolEntry constructs a read-pool entry with kit-default
// initial atomic state (healthy=true, lagMillis=-1 meaning "no probe
// yet"). Used by Connect to seed every entry before the lag-polling
// goroutine has had a chance to write real values.
func newReadPoolEntry(name string, p *pgxpool.Pool) *readPoolEntry {
	e := &readPoolEntry{name: name, pool: p}
	e.healthy.Store(true)
	e.lagMillis.Store(-1)
	return e
}

// connectPool opens one pool against raw, applies cfg knobs and the tracer
// from o, and runs the retry loop. name ("primary" or "standby") is used as
// the pool label when attaching to metrics. Returns
// *errs.Error{Kind:KindUnavailable} on exhausted budget or ctx cancellation
// during backoff.
func connectPool(ctx context.Context, raw, name string, cfg Config, o *options) (*pgxpool.Pool, error) {
	pgxCfg, err := pgxpool.ParseConfig(raw)
	if err != nil {
		return nil, errs.Wrap(err, errs.KindInternal, "db_config_invalid", "could not parse db config")
	}
	if cfg.MaxConns > 0 {
		pgxCfg.MaxConns = cfg.MaxConns
	}
	if cfg.MinConns > 0 {
		pgxCfg.MinConns = cfg.MinConns
	}
	if cfg.MaxConnLifetime > 0 {
		pgxCfg.MaxConnLifetime = cfg.MaxConnLifetime
	}
	if cfg.MaxConnIdle > 0 {
		pgxCfg.MaxConnIdleTime = cfg.MaxConnIdle
	}
	if t := composeTracer(o); t != nil {
		pgxCfg.ConnConfig.Tracer = t
	}
	if hook := composeAfterConnect(o); hook != nil {
		pgxCfg.AfterConnect = hook
	}

	var pool *pgxpool.Pool
	for attempt := 0; attempt <= cfg.ConnectMaxRetries; attempt++ {
		if attempt > 0 {
			wait := backoffWait(attempt, cfg.ConnectBackoffBase, cfg.ConnectBackoffMax)
			if o.logger != nil {
				o.logger.Warn("db: connect failed, retrying",
					"attempt", attempt,
					"max_retries", cfg.ConnectMaxRetries,
					"wait", wait,
					"err", err)
			}
			select {
			case <-ctx.Done():
				return nil, errs.Wrap(ctx.Err(), errs.KindUnavailable, "db_unavailable", "connect cancelled")
			case <-time.After(wait):
			}
		}
		pool, err = pgxpool.NewWithConfig(ctx, pgxCfg)
		if err != nil {
			continue
		}
		pingCtx := ctx
		if cfg.ConnectTimeout > 0 {
			var cancel context.CancelFunc
			pingCtx, cancel = context.WithTimeout(ctx, cfg.ConnectTimeout)
			err = pool.Ping(pingCtx)
			cancel()
		} else {
			err = pool.Ping(pingCtx)
		}
		if err != nil {
			pool.Close()
			pool = nil
			continue
		}
		break
	}
	if err != nil {
		return nil, errs.Wrap(err, errs.KindUnavailable, "db_unavailable", "could not reach db")
	}

	if o.metrics != nil {
		o.metrics.attach(name, pool)
	}
	return pool, nil
}

// backoffWait returns the wait duration before attempt N (1-indexed).
// Exponential: base << (N-1), capped at max. Returns 0 if base <= 0.
func backoffWait(attempt int, base, max time.Duration) time.Duration {
	if base <= 0 {
		return 0
	}
	w := base << (attempt - 1)
	if w <= 0 || w > max {
		return max
	}
	return w
}

// Close releases the primary pool and every read pool when present.
// Stops the lag-polling goroutine when [WithReplicaLagPolling] was
// wired. Safe to call multiple times.
func (d *DB) Close() {
	d.stopLagPolling()
	for _, e := range d.readPools {
		if e.pool != nil {
			e.pool.Close()
		}
	}
	d.readPools = nil
	if d.pool == nil {
		return
	}
	d.pool.Close()
	d.pool = nil
}

// Pool returns the underlying *pgxpool.Pool for advanced use (COPY,
// custom isolation, direct pgx APIs). Errors via this path are NOT
// funneled through mapPgxErr — the caller owns mapping.
func (d *DB) Pool() *pgxpool.Pool { return d.pool }

// Query executes sql and returns the rows. The error is funneled through mapPgxErr.
func (d *DB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return doQuery(ctx, d.pool, sql, args...)
}

// QueryRow executes sql and returns a single row. The row's Scan error is
// funneled through mapPgxErr.
func (d *DB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return doQueryRow(ctx, d.pool, sql, args...)
}

// Exec executes sql. The error is funneled through mapPgxErr.
func (d *DB) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return doExec(ctx, d.pool, sql, args...)
}

// ReadQuery runs sql against a read-replica pool when at least one is
// configured; otherwise falls back to the primary pool. Use for SELECTs
// that tolerate replica lag — listings, search, analytics, plain GETs.
// NEVER use for SELECT FOR UPDATE or queries that must see just-written
// data; use Query for those.
//
// Routing across multiple replicas follows [Config.ReadStrategy]
// (round_robin by default). The kit does NOT skip lagging or unhealthy
// pools mid-flight — wire [WithReplicaLagPolling] for visibility and
// remove problem replicas from ReadURLs at deploy time when needed.
//
// Wrap ctx with [ReadFromPrimary] to force the primary pool — useful
// for read-your-writes immediately after a write transaction.
func (d *DB) ReadQuery(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return doQuery(ctx, d.pickReadPool(ctx), sql, args...)
}

// ReadQueryRow is the single-row companion to ReadQuery; same semantics.
func (d *DB) ReadQueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return doQueryRow(ctx, d.pickReadPool(ctx), sql, args...)
}

// pickReadPool implements the routing decision used by ReadQuery /
// ReadQueryRow. Returns the primary pool when no replicas are
// configured OR the ctx carries the [ReadFromPrimary] marker;
// otherwise picks via [Config.ReadStrategy], skipping replicas marked
// unhealthy or carrying lag above [WithReadLagBudget].
//
// When every replica is filtered out (all unhealthy, all over budget,
// or both), the kit falls back to the primary rather than failing the
// query — favouring availability over the "read-only intent" of the
// caller. The skip event is counted in `db_replica_skipped_total`
// when [WithMetrics] is wired so dashboards can spot the degradation.
func (d *DB) pickReadPool(ctx context.Context) *pgxpool.Pool {
	if readFromPrimaryRequested(ctx) || len(d.readPools) == 0 {
		return d.pool
	}
	eligible := d.eligibleReadPools()
	if len(eligible) == 0 {
		// Every replica is filtered out — fall back to primary.
		d.opts.metrics.incReplicaFallback()
		return d.pool
	}
	if len(eligible) == 1 {
		return eligible[0].pool
	}
	switch d.route {
	case routeRandom:
		// rand.IntN is goroutine-safe (math/rand/v2 uses an internal
		// per-thread PRNG); no synchronisation needed on the hot path.
		return eligible[rand.IntN(len(eligible))].pool
	default: // routeRoundRobin
		n := d.nextRead.Add(1) - 1
		return eligible[int(n%uint64(len(eligible)))].pool
	}
}

// eligibleReadPools filters the read pools by health + lag budget. The
// returned slice is a fresh view — never mutated by the caller; the
// kit allocates it lazily only when filtering is necessary.
//
// Fast path: when neither WithReadLagBudget is configured AND every
// pool is healthy, the function returns the underlying slice without
// allocating.
func (d *DB) eligibleReadPools() []*readPoolEntry {
	budgetMS := int64(0)
	if d.opts.readLagBudget > 0 {
		budgetMS = d.opts.readLagBudget.Milliseconds()
	}
	allHealthy := true
	if budgetMS == 0 {
		// Only health matters; quick scan.
		for _, e := range d.readPools {
			if !e.healthy.Load() {
				allHealthy = false
				break
			}
		}
		if allHealthy {
			return d.readPools
		}
	}
	out := make([]*readPoolEntry, 0, len(d.readPools))
	for _, e := range d.readPools {
		if !e.healthy.Load() {
			d.opts.metrics.incReplicaSkipped(e.name, "unhealthy")
			continue
		}
		if budgetMS > 0 {
			lag := e.lagMillis.Load()
			// lag == -1 means "no probe yet"; treat as healthy +
			// within budget (kit favours optimism — a freshly-started
			// replica is not necessarily over budget).
			if lag >= 0 && lag > budgetMS {
				d.opts.metrics.incReplicaSkipped(e.name, "over_budget")
				continue
			}
		}
		out = append(out, e)
	}
	return out
}

// ReadPools returns every configured read-replica pool together with its
// stable display name ("standby" when [Config.HasReadReplica] was set,
// "standby-1" / "standby-2" / … in [Config.ReadURLs] index order).
// Empty slice when no replica is configured.
func (d *DB) ReadPools() []ReadPoolInfo {
	out := make([]ReadPoolInfo, 0, len(d.readPools))
	for _, e := range d.readPools {
		out = append(out, ReadPoolInfo{
			Name:       e.name,
			Pool:       e.pool,
			Healthy:    e.healthy.Load(),
			LagSeconds: lagMillisToSeconds(e.lagMillis.Load()),
		})
	}
	return out
}

// lagMillisToSeconds projects the atomic int64 lagMillis back into a
// float seconds value for the public API. -1 → 0 (no probe yet); kit
// treats "unknown lag" as 0 in the surface so callers don't need a
// special case.
func lagMillisToSeconds(ms int64) float64 {
	if ms < 0 {
		return 0
	}
	return float64(ms) / 1000.0
}

// ReadPoolInfo is the projection returned by [DB.ReadPools]. Name
// matches the `pool=` label used in the kit's Prometheus collectors.
//
// Healthy and LagSeconds reflect the most recent
// [WithReplicaLagPolling] probe. When polling is not wired, every
// entry reports Healthy=true and LagSeconds=0 (kit-default initial
// state).
type ReadPoolInfo struct {
	Name       string
	Pool       *pgxpool.Pool
	Healthy    bool
	LagSeconds float64
}

var _ Querier = (*DB)(nil)
