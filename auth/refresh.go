package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"time"

	xerrs "github.com/theizzatbek/gokit/errs"
)

// rawRefreshPrefix is prepended to the wire token purely for log readability —
// it does NOT participate in hashing/lookup. The 32-byte body that follows is
// the actual secret.
const rawRefreshPrefix = "rt_"

// Record is one row in a refresh-token journal. RefreshStore implementations
// persist these. Raw tokens are NEVER stored — only SHA-256 hashes.
type Record struct {
	TokenHash  [32]byte
	Subject    string
	FamilyID   string
	ParentHash [32]byte
	IssuedAt   time.Time
	ExpiresAt  time.Time
	ConsumedAt *time.Time
	RevokedAt  *time.Time
	UserAgent  string
	IP         string
}

// RefreshStore is the pluggable persistence layer for refresh tokens. Two
// production implementations ship under auth/refreshpg and auth/refreshredis.
// In-memory implementation for tests lives at auth/internal/memstore.
//
// Implementations MUST return *errs.Error (Code constants from errors.go) for
// the documented failure modes — Bearer/Refresh handlers switch on them.
type RefreshStore interface {
	Issue(ctx context.Context, r Record) error

	// Consume is atomic: validates the record (exists, not consumed, not revoked,
	// not expired), marks it consumed, and returns it. On already-consumed or
	// revoked: returns *errs.Error{Code: CodeRefreshReused} AND calls
	// RevokeFamily(r.FamilyID) before returning — this is OAuth 2.1 reuse
	// detection. On expired: CodeRefreshExpired. On not-found: CodeRefreshInvalid.
	Consume(ctx context.Context, tokenHash [32]byte, now time.Time) (Record, error)

	RevokeFamily(ctx context.Context, familyID string) error
	RevokeSubject(ctx context.Context, subject string) error
	GarbageCollect(ctx context.Context, now time.Time) (int64, error)
}

// TokenRevoker is an optional RefreshStore extension: explicit revocation of
// a single refresh token by hash, without consuming it or issuing a
// replacement. All kit stores (refreshpg, refreshredis, the internal test
// memstore) implement it; Auth.RevokeRefresh / Auth.RevokeFamily prefer this
// path and fall back to a Consume-based emulation when the configured store
// predates the interface.
//
// Kept as a separate interface rather than a new RefreshStore method so that
// existing third-party RefreshStore implementations stay compatible (the
// api-compat CI gate forbids adding methods to exported interfaces). Folding
// it into RefreshStore is queued for v2 — see docs/v2-backlog.md.
type TokenRevoker interface {
	// RevokeToken marks the single record matching tokenHash revoked, the
	// same way reuse-detection does — a later Consume of this token reports
	// CodeRefreshReused. Idempotent: an already consumed/revoked/expired
	// record is still returned with found=true (an existing revocation
	// timestamp is preserved); a missing record returns found=false and a
	// nil error. A non-nil error means a transient store failure ONLY.
	//
	// The returned Record reliably carries identity fields (FamilyID, Subject,
	// issue/expiry timestamps); whether RevokedAt/ConsumedAt are populated is
	// store-specific — callers must not rely on them.
	RevokeToken(ctx context.Context, tokenHash [32]byte, now time.Time) (rec Record, found bool, err error)
}

// newRawRefresh generates a fresh wire token and its SHA-256 hash.
// The raw form goes in the cookie; the hash goes in the store.
func newRawRefresh() (raw string, hash [32]byte, err error) {
	var body [32]byte
	if _, err := rand.Read(body[:]); err != nil {
		return "", [32]byte{}, xerrs.Wrap(err, xerrs.KindInternal, "rand_failed", "refresh token rng")
	}
	raw = rawRefreshPrefix + base64.RawURLEncoding.EncodeToString(body[:])
	hash = hashRefresh(raw)
	return raw, hash, nil
}

// hashRefresh derives the on-disk identifier for a raw refresh token. The
// prefix is hashed too — that's fine, it just means the store key space is
// deterministic per kit version.
func hashRefresh(raw string) [32]byte {
	return sha256.Sum256([]byte(raw))
}
