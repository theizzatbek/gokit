package s3mod

import (
	"context"
	"fmt"

	s3client "github.com/theizzatbek/gokit/clients/s3"
	xerrs "github.com/theizzatbek/gokit/errs"
	"github.com/theizzatbek/gokit/svckit"
)

// CodeConnectFailed — s3client.Connect could not connect.
const CodeConnectFailed = "s3mod_connect_failed"

// DefaultName — the mod's name unless overridden via WithName.
const DefaultName = "s3"

// Config — S3 configuration. Alias of the client's own type: the
// env tags already live there, no point duplicating them.
//
//	type AppConfig struct {
//	    svckit.Config
//	    S3 s3mod.Config `envPrefix:"S3_"`
//	}
type Config = s3client.Config

// Option configures the mod.
type Option func(*Mod)

// WithName sets the mod's name. Needed when a single service runs
// two S3 mods — e.g. the primary bucket and a backup bucket.
func WithName(name string) Option {
	return func(m *Mod) { m.name = name }
}

// WithClientOptions forwards options to s3client.Connect. The mod
// wires the logger and metrics from Host itself.
func WithClientOptions(opts ...s3client.Option) Option {
	return func(m *Mod) { m.clientOpts = append(m.clientOpts, opts...) }
}

// Mod is the S3 handle. Created before svckit.New, handed to it via
// Option(), and holds the client once the build succeeds.
type Mod struct {
	name       string
	cfg        Config
	clientOpts []s3client.Option

	client *s3client.Client
	built  bool
}

// New creates the handle. An empty Config.Bucket means "the operator
// did not enable S3": the mod stays disabled, and Client() names
// exactly which env var is responsible.
func New(cfg Config, opts ...Option) *Mod {
	m := &Mod{name: DefaultName, cfg: cfg}
	for _, o := range opts {
		o(m)
	}
	return m
}

// Option wires the mod into svckit.New.
func (m *Mod) Option() svckit.Option { return svckit.WithMod(m) }

// Name implements svckit.Mod.
func (m *Mod) Name() string { return m.name }

// Build implements svckit.Builder.
func (m *Mod) Build(ctx context.Context, h svckit.Host) error {
	m.built = true
	if m.cfg.Bucket == "" {
		return nil // operator did not enable S3 — not an error
	}
	defaults := []s3client.Option{s3client.WithLogger(h.Logger())}
	if h.Metrics() != nil {
		defaults = append(defaults, s3client.WithMetrics(h.Metrics()))
	}
	cli, err := s3client.Connect(ctx, m.cfg, append(defaults, m.clientOpts...)...)
	if err != nil {
		return xerrs.Wrap(err, xerrs.KindUnavailable, CodeConnectFailed,
			"s3mod: connect failed")
	}
	m.client = cli
	return nil
}

// Status implements svckit.Statuser.
func (m *Mod) Status() any {
	if m.client == nil {
		return nil
	}
	return map[string]string{"bucket": m.cfg.Bucket}
}

// Enabled reports whether the client came up.
func (m *Mod) Enabled() bool { return m.client != nil }

// Optional is the soft-access form for code that can run without S3.
func (m *Mod) Optional() (*s3client.Client, bool) { return m.client, m.client != nil }

// Client returns the client or panics with a message naming exactly
// one of the two possible causes: the mod wasn't passed to
// svckit.New, or the bucket isn't configured.
func (m *Mod) Client() *s3client.Client {
	if !m.built {
		panic(fmt.Sprintf(
			"s3mod: Client() called before build — pass %s.Option() to svckit.New", m.name))
	}
	if m.client == nil {
		panic("s3mod: Client() called but S3 is not configured (empty Bucket — set S3_BUCKET)")
	}
	return m.client
}
