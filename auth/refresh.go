package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"time"

	xerrs "github.com/theizzatbek/fibermap/errs"
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
