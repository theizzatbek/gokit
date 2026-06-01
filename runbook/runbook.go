package runbook

import (
	"context"
	"regexp"
	"sync"
	"sync/atomic"
	"time"

	"github.com/theizzatbek/gokit/audit"
	xerrs "github.com/theizzatbek/gokit/errs"
)

// Store is the persistence interface for [Runbook] flags.
// Implementations span: in-process map for tests / single-pod
// services, Redis for multi-pod fleets, Postgres when you want
// flag-flip history persisted alongside the rest of the schema.
//
// All methods MUST respect ctx.Done().
type Store interface {
	// Get returns the boolean stored for name. A flag with no
	// stored value returns (true, true) — kit-wide default-on
	// semantics — so a missing-config blip never disables a
	// feature. (The bool's second return is "found-or-not";
	// implementations that want "missing = disabled" can return
	// (false, true).)
	Get(ctx context.Context, name string) (enabled, found bool, err error)

	// Set persists name=enabled. Stores that can't write (in-mem
	// readonly mode) return an error.
	Set(ctx context.Context, name string, enabled bool) error

	// All returns every stored flag for the admin UI. Stores that
	// can't enumerate (a pure KV with no scan) MAY return
	// (nil, nil).
	All(ctx context.Context) (map[string]bool, error)
}

// Runbook is the public API. Construct with [New]; goroutine-safe.
type Runbook struct {
	store    Store
	auditor  *audit.Logger
	logger   logFn
	interval time.Duration

	mu    sync.RWMutex
	cache map[string]bool

	closed atomic.Bool
	stop   chan struct{}
	done   chan struct{}
}

// Option tunes [New].
type Option func(*Runbook)

// WithAuditor wires an audit logger. Every Set emits one
// "runbook.flag_changed" event so the compliance trail captures
// who flipped what when.
func WithAuditor(l *audit.Logger) Option {
	return func(r *Runbook) { r.auditor = l }
}

// WithRefreshInterval enables periodic refresh of the in-process
// cache from the Store. Default 0 — cache only updates on Set.
// Set to >0 in multi-pod fleets where one pod's Set should
// propagate to others within the interval.
func WithRefreshInterval(d time.Duration) Option {
	return func(r *Runbook) { r.interval = d }
}

// logFn is the minimal logger surface we accept — avoids dragging
// slog into callers that don't have one. Implementations should be
// cheap; we log at most one line per Set.
type logFn func(msg string, kv ...any)

// WithLogger installs a logFn that receives one line per Set + per
// refresh-cycle error. Wire e.g. svc.Logger().Info.
func WithLogger(fn logFn) Option {
	return func(r *Runbook) { r.logger = fn }
}

// New constructs a Runbook bound to store. Loads the initial cache
// snapshot synchronously so the first Enabled call doesn't race
// against an empty cache.
//
// Returns *errs.Error{Code: CodeNilStore} when store is nil.
func New(store Store, opts ...Option) (*Runbook, error) {
	if store == nil {
		return nil, xerrs.Validation(CodeNilStore, "runbook: nil Store")
	}
	r := &Runbook{
		store: store,
		cache: map[string]bool{},
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}
	for _, opt := range opts {
		opt(r)
	}
	// Warm cache once.
	if all, err := store.All(context.Background()); err == nil {
		r.mu.Lock()
		for k, v := range all {
			r.cache[k] = v
		}
		r.mu.Unlock()
	}
	if r.interval > 0 {
		go r.refreshLoop()
	} else {
		close(r.done)
	}
	return r, nil
}

// Enabled reports whether the named flag is enabled. Default-on:
// a flag with no stored value returns true. Cheap — O(1) in-process
// read.
func (r *Runbook) Enabled(_ context.Context, name string) bool {
	r.mu.RLock()
	v, ok := r.cache[name]
	r.mu.RUnlock()
	if !ok {
		return true // default-on
	}
	return v
}

// SetEnabled persists name=enabled, updates the in-process cache,
// and emits an audit event when an auditor is wired. Errors come
// from the Store.
func (r *Runbook) SetEnabled(ctx context.Context, name string, enabled bool, actor audit.Actor) error {
	if !validFlagName(name) {
		return xerrs.Validationf(CodeInvalidFlagName,
			"runbook: invalid flag name %q (want [a-z0-9_.:-]{1,64})", name)
	}
	if err := r.store.Set(ctx, name, enabled); err != nil {
		return xerrs.Wrap(err, xerrs.KindUnavailable, CodeStoreFailed,
			"runbook: store set failed")
	}
	r.mu.Lock()
	r.cache[name] = enabled
	r.mu.Unlock()
	if r.logger != nil {
		r.logger("runbook: flag changed", "name", name, "enabled", enabled, "actor", actor.Subject)
	}
	if r.auditor != nil {
		outcome := audit.Success
		_, _ = r.auditor.Log(ctx, audit.Event{
			Action:   "runbook.flag_changed",
			Actor:    actor,
			Target:   audit.Target{Type: "runbook_flag", ID: name},
			Outcome:  outcome,
			Metadata: map[string]any{"enabled": enabled},
		})
	}
	return nil
}

// All returns a snapshot of the current cache. Useful for the
// admin UI; production code should prefer [Enabled].
func (r *Runbook) All() map[string]bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]bool, len(r.cache))
	for k, v := range r.cache {
		out[k] = v
	}
	return out
}

// Close stops the refresh loop. Idempotent.
func (r *Runbook) Close() {
	if !r.closed.CompareAndSwap(false, true) {
		return
	}
	close(r.stop)
	<-r.done
}

func (r *Runbook) refreshLoop() {
	defer close(r.done)
	t := time.NewTicker(r.interval)
	defer t.Stop()
	for {
		select {
		case <-r.stop:
			return
		case <-t.C:
			all, err := r.store.All(context.Background())
			if err != nil {
				if r.logger != nil {
					r.logger("runbook: refresh failed", "err", err.Error())
				}
				continue
			}
			r.mu.Lock()
			r.cache = make(map[string]bool, len(all))
			for k, v := range all {
				r.cache[k] = v
			}
			r.mu.Unlock()
		}
	}
}

// flagNameRE matches the kit's flag-name shape — short identifier-
// like, no spaces, plus `:`/`.`/`-` so callers can namespace
// (e.g. "checkout:v2", "billing.charges").
var flagNameRE = regexp.MustCompile(`^[a-z0-9_.:-]{1,64}$`)

func validFlagName(s string) bool { return flagNameRE.MatchString(s) }
