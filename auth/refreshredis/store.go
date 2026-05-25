// Package refreshredis implements auth.RefreshStore on top of Redis.
// Each refresh record lives in one HASH; family + subject sets give the
// O(1) bulk-revoke paths. All TTLs are managed via EXPIREAT.
package refreshredis

import (
	"context"
	"encoding/hex"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/theizzatbek/fibermap/auth"
	"github.com/theizzatbek/fibermap/errs"
)

// Store is a Redis-backed RefreshStore. Client ownership stays with the caller.
type Store struct{ c *redis.Client }

// New wraps an existing *redis.Client.
func New(c *redis.Client) *Store { return &Store{c: c} }

func refreshKey(h [32]byte) string { return "refresh:" + hashToHex(h) }
func familyKey(id string) string   { return "refresh:family:" + id }
func subjectKey(s string) string   { return "refresh:subject:" + s }
func hashToHex(h [32]byte) string  { return hex.EncodeToString(h[:]) }

// Issue creates the hash + family/subject set entries with EXPIREAT.
func (s *Store) Issue(ctx context.Context, r auth.Record) error {
	if r.ExpiresAt.IsZero() {
		return errs.Validation("missing_expiry", "Record.ExpiresAt required")
	}
	pipe := s.c.TxPipeline()
	pipe.HSet(ctx, refreshKey(r.TokenHash), map[string]any{
		"family":      r.FamilyID,
		"parent_hash": hex.EncodeToString(r.ParentHash[:]),
		"subject":     r.Subject,
		"issued_at":   r.IssuedAt.Unix(),
		"expires_at":  r.ExpiresAt.Unix(),
		"user_agent":  r.UserAgent,
		"ip":          r.IP,
		"consumed":    "0",
		"revoked":     "0",
	})
	pipe.ExpireAt(ctx, refreshKey(r.TokenHash), r.ExpiresAt)
	pipe.SAdd(ctx, familyKey(r.FamilyID), hashToHex(r.TokenHash))
	pipe.ExpireAt(ctx, familyKey(r.FamilyID), r.ExpiresAt.Add(time.Hour))
	if r.Subject != "" {
		pipe.SAdd(ctx, subjectKey(r.Subject), hashToHex(r.TokenHash))
		pipe.ExpireAt(ctx, subjectKey(r.Subject), r.ExpiresAt.Add(time.Hour))
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return errs.Wrap(err, errs.KindUnavailable, auth.CodeStoreUnavailable, "redis refresh issue failed")
	}
	return nil
}

// Consume / RevokeFamily / RevokeSubject / GarbageCollect — Tasks 23 and 24.
