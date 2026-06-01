package audit

import (
	"context"
	"time"

	xerrs "github.com/theizzatbek/gokit/errs"
)

// Store is the persistence interface. Implementations MUST honour
// ctx.Done() and may be called concurrently from multiple goroutines.
//
// Append MUST set Event.ID (server-generated UUID) and persist the
// event atomically. When the store opts into hash-chaining,
// implementations MUST serialize Append calls (advisory lock,
// row-lock, or stream-of-one) so two writers don't fork the chain.
type Store interface {
	Append(ctx context.Context, e *Event) error
	Query(ctx context.Context, f Filter) ([]Event, error)
	// LastHash returns the Hash of the most recent event, or nil
	// when the chain is empty. Used by hash-chain-mode Append to
	// seed PrevHash.
	LastHash(ctx context.Context) ([]byte, error)
	// PurgeBefore deletes events with OccurredAt < t. Returns the
	// number of rows deleted. Stores that don't support deletion
	// (immutable backends — WORM storage) return an error.
	PurgeBefore(ctx context.Context, t time.Time) (int64, error)
	// ChainLock serializes hash-chain Append calls across writers.
	// Implementations that span multiple processes (Postgres) MUST
	// acquire a cross-process lock (db/lock) so two writers cannot
	// fork the chain. Single-process stores (MemoryStore) return
	// a no-op release function — the Logger's in-process mutex is
	// enough.
	//
	// Contract: the caller invokes ChainLock → LastHash → builds
	// the event with the returned PrevHash → Append → release().
	// The lock covers the entire critical section.
	ChainLock(ctx context.Context) (release func(), err error)
}

// Filter narrows a Query. Zero-value fields are wildcards. Limit 0
// is treated as "no limit" by stores that allow it; production
// callers should always set a Limit to keep queries bounded.
type Filter struct {
	// Actor matches Event.Actor.Subject exactly.
	Actor string

	// Action matches Event.Action — supports trailing "*" wildcard
	// (e.g. "user.*"). Implementations that can't do wildcards
	// may return a `not_implemented`-shaped error.
	Action string

	// TargetType / TargetID narrow by Event.Target fields.
	TargetType string
	TargetID   string

	// Outcome restricts to one Outcome. Empty matches all.
	Outcome Outcome

	// From / To bound OccurredAt. Zero values are open.
	From time.Time
	To   time.Time

	// Limit caps the result-set size. 0 = no cap (store-dependent).
	Limit int

	// Offset for paging. Together with Limit + ORDER BY occurred_at
	// gives a stable iterator.
	Offset int
}

// newInvalidEventError is a tiny constructor reused by validate()
// so the same Code shows up on every validation miss.
func newInvalidEventError(msg string) error {
	return xerrs.Validation(CodeInvalidEvent, "audit: "+msg)
}
