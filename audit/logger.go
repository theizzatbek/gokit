package audit

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	xerrs "github.com/theizzatbek/gokit/errs"
)

// Config tunes a [Logger].
type Config struct {
	// ServiceName is stamped on every event so a shared audit
	// table (multi-service deployment) stays disambiguatable.
	// Empty is allowed for single-service services.
	ServiceName string

	// Logger receives Warn entries on Append failures. The audit
	// trail itself is the source of truth; this just surfaces
	// transport blips to ops.
	Logger *slog.Logger
}

// Option tunes [New].
type Option func(*Logger)

// WithHashChain enables tamper-evident chaining: every Append sets
// PrevHash to LastHash from the Store and computes Hash =
// SHA256(canonical(event) || PrevHash). Auditors can later call
// [Verify] to walk the chain end-to-end.
//
// Cost: each Append serializes (the store implementation is
// responsible for the lock) so multi-writer throughput drops.
// Use only when the regulatory requirement explicitly demands
// tamper-evidence — most apps don't need it.
func WithHashChain() Option {
	return func(l *Logger) { l.hashChain = true }
}

// Logger is the public audit-emit API. Construct with [New];
// goroutine-safe.
type Logger struct {
	store     Store
	cfg       Config
	hashChain bool

	// chainMu serializes hash-chain Append calls within this
	// process. Multi-process serialization is the store's
	// responsibility (advisory lock).
	chainMu sync.Mutex
}

// New returns a Logger bound to store. Returns *errs.Error{Code:
// CodeNilStore} when store is nil — silent no-op'ing audit logs
// would mask the compliance gap, refusing fails loud.
func New(store Store, cfg Config, opts ...Option) (*Logger, error) {
	if store == nil {
		return nil, xerrs.Validation(CodeNilStore, "audit: nil Store")
	}
	l := &Logger{store: store, cfg: cfg}
	for _, opt := range opts {
		opt(l)
	}
	return l, nil
}

// Log records e to the underlying store. Server-side fields
// (ID, OccurredAt, ServiceName, Hash) are populated automatically
// when zero. Returns the assigned event ID + nil on success.
//
// Hash-chain mode: serializes through chainMu, reads LastHash from
// the store, computes Hash, then Append's atomically. The store is
// expected to take its own lock so two processes don't race.
func (l *Logger) Log(ctx context.Context, e Event) (string, error) {
	if err := e.validate(); err != nil {
		return "", err
	}
	if e.ID == "" {
		e.ID = uuid.NewString()
	}
	suppliedOccurredAt := !e.OccurredAt.IsZero()
	if !suppliedOccurredAt {
		e.OccurredAt = time.Now()
	}
	// Truncate to microseconds so the value stored in Postgres
	// (timestamptz is µs-precision) round-trips identically through
	// Query → Verify. Go's time.Now() has ns precision; RFC3339Nano in
	// canonicalHashInput would otherwise include nanoseconds the
	// database silently drops, breaking the chain on readback.
	e.OccurredAt = e.OccurredAt.Truncate(time.Microsecond)
	if e.ServiceName == "" {
		e.ServiceName = l.cfg.ServiceName
	}
	if l.hashChain {
		// Three layers of serialization in chain mode:
		//   1. chainMu — in-process. Cheap, kept around even when
		//      the store doesn't need it.
		//   2. store.ChainLock — cross-process. Postgres uses an
		//      advisory lock; MemoryStore returns no-op.
		//   3. The (LastHash → ComputeHash → Append) sequence
		//      runs inside both locks so two writers cannot fork
		//      the chain by reading the same LastHash + inserting
		//      two events with PrevHash=Hprev.
		// OccurredAt is ALSO set inside the lock so Query's
		// ORDER BY occurred_at ASC matches chain order on
		// readback.
		l.chainMu.Lock()
		defer l.chainMu.Unlock()
		release, err := l.store.ChainLock(ctx)
		if err != nil {
			return "", xerrs.Wrap(err, xerrs.KindUnavailable, CodeAppendFailed,
				"audit: chain lock acquire failed")
		}
		defer release()
		// Re-stamp OccurredAt inside the lock for monotonic-
		// w.r.t.-lock order. If the caller supplied a non-zero
		// OccurredAt explicitly (test fixtures with crafted
		// time) keep it — they know the chain may not order by
		// time.
		// Note: OccurredAt was set earlier (above the chain block)
		// when caller didn't supply one. For chain-mode + auto
		// timestamps, re-stamp INSIDE the lock so the in-lock
		// sequence is monotonic. Caller-supplied OccurredAt is
		// preserved — fixtures sometimes craft a specific time.
		if !suppliedOccurredAt {
			// Same µs-truncation rule as the auto-stamp above so the
			// hash committed under the lock matches the Postgres-side
			// timestamptz precision on readback.
			e.OccurredAt = time.Now().Truncate(time.Microsecond)
		}
		prev, err := l.store.LastHash(ctx)
		if err != nil {
			return "", xerrs.Wrap(err, xerrs.KindUnavailable, CodeAppendFailed,
				"audit: last-hash lookup failed")
		}
		e.PrevHash = prev
		h, err := e.computeHash()
		if err != nil {
			return "", xerrs.Wrap(err, xerrs.KindInternal, CodeAppendFailed,
				"audit: hash compute failed")
		}
		e.Hash = h
	}
	if err := l.store.Append(ctx, &e); err != nil {
		if l.cfg.Logger != nil {
			l.cfg.Logger.Warn("audit: append failed",
				"action", e.Action, "actor", e.Actor.Subject,
				"err", err.Error())
		}
		return "", xerrs.Wrap(err, xerrs.KindUnavailable, CodeAppendFailed,
			"audit: append failed")
	}
	return e.ID, nil
}

// Login is the typed convenience for authentication events.
// outcome = Success / Failure (bad password) / Denied (locked).
func (l *Logger) Login(ctx context.Context, actor Actor, outcome Outcome) error {
	_, err := l.Log(ctx, Event{
		Action: "auth.login", Actor: actor, Outcome: outcome,
	})
	return err
}

// Logout records an authenticated session-end.
func (l *Logger) Logout(ctx context.Context, actor Actor) error {
	_, err := l.Log(ctx, Event{
		Action: "auth.logout", Actor: actor, Outcome: Success,
	})
	return err
}

// Created records a resource-creation event.
func (l *Logger) Created(ctx context.Context, actor Actor, target Target) error {
	_, err := l.Log(ctx, Event{
		Action: target.Type + ".created", Actor: actor, Target: target,
		Outcome: Success,
	})
	return err
}

// Updated records a resource-mutation. Diff is folded into
// Metadata.diff so admin tools can show before/after.
func (l *Logger) Updated(ctx context.Context, actor Actor, target Target, diff map[string]any) error {
	meta := map[string]any{}
	if len(diff) > 0 {
		meta["diff"] = diff
	}
	_, err := l.Log(ctx, Event{
		Action: target.Type + ".updated", Actor: actor, Target: target,
		Outcome: Success, Metadata: meta,
	})
	return err
}

// Deleted records a resource removal.
func (l *Logger) Deleted(ctx context.Context, actor Actor, target Target) error {
	_, err := l.Log(ctx, Event{
		Action: target.Type + ".deleted", Actor: actor, Target: target,
		Outcome: Success,
	})
	return err
}

// Denied records an authorization rejection. `action` is the verb
// the actor was attempting (e.g. "post.delete"); reason is the
// kit-internal denial code ("not_owner", "missing_scope").
func (l *Logger) Denied(ctx context.Context, actor Actor, target Target, action, reason string) error {
	_, err := l.Log(ctx, Event{
		Action: action, Actor: actor, Target: target, Outcome: Denied,
		Metadata: map[string]any{"reason": reason},
	})
	return err
}

// Query is a passthrough to the Store with error mapping.
func (l *Logger) Query(ctx context.Context, f Filter) ([]Event, error) {
	out, err := l.store.Query(ctx, f)
	if err != nil {
		return nil, xerrs.Wrap(err, xerrs.KindUnavailable, CodeQueryFailed,
			"audit: query failed")
	}
	return out, nil
}

// PurgeBefore wraps Store.PurgeBefore with the kit's error shape.
// Use from a periodic job (db/jobs + a retention policy).
func (l *Logger) PurgeBefore(ctx context.Context, t time.Time) (int64, error) {
	n, err := l.store.PurgeBefore(ctx, t)
	if err != nil {
		return 0, xerrs.Wrap(err, xerrs.KindUnavailable, CodePurgeFailed,
			"audit: purge failed")
	}
	return n, nil
}

// Verify walks the chain in OccurredAt-ASC order and reports the
// first broken link, or nil when the chain is intact.
//
// Only meaningful when WithHashChain was used at write-time —
// otherwise Hash + PrevHash are nil and every event "verifies"
// trivially.
func Verify(events []Event) error {
	var prev []byte
	for i := range events {
		e := events[i]
		if !bytesEqual(e.PrevHash, prev) {
			return xerrs.Validationf(CodeChainBroken,
				"event %d: PrevHash mismatch at id=%s", i, e.ID)
		}
		want, err := e.computeHash()
		if err != nil {
			return xerrs.Wrap(err, xerrs.KindInternal, CodeChainBroken,
				"event hash compute failed")
		}
		if !bytesEqual(want, e.Hash) {
			return xerrs.Validationf(CodeChainBroken,
				"event %d: Hash mismatch at id=%s", i, e.ID)
		}
		prev = e.Hash
	}
	return nil
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
