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

// consumeScript atomically:
//  1. EXISTS check — if missing, returns "missing"
//  2. consumed/revoked check — if either is "1", returns "reused" AND iterates
//     the family set, marking every member revoked
//  3. expires_at check — if <= now, returns "expired"
//  4. otherwise: sets consumed=1 and returns the body fields needed by Go
//
// Return shape on success: { "ok", family, parent_hash, subject, issued_at, user_agent, ip, expires_at }
// Return shape on error:   { "missing" } | { "reused" } | { "expired" }
var consumeScript = redis.NewScript(`
local key = KEYS[1]
local now = tonumber(ARGV[1])
if redis.call("EXISTS", key) == 0 then return {"missing"} end
local h = redis.call("HMGET", key, "consumed","revoked","expires_at","family","parent_hash","subject","issued_at","user_agent","ip")
local consumed, revoked, exp = h[1], h[2], tonumber(h[3])
if consumed == "1" or revoked == "1" then
    local family = h[4]
    local fkey = "refresh:family:"..family
    local members = redis.call("SMEMBERS", fkey)
    for i=1,#members do
        redis.call("HSET", "refresh:"..members[i], "revoked", "1")
    end
    return {"reused"}
end
if exp <= now then return {"expired"} end
redis.call("HSET", key, "consumed", "1")
return {"ok", h[4], h[5], h[6], h[7], h[8], h[9], tostring(exp)}
`)

// Consume runs consumeScript atomically against Redis.
func (s *Store) Consume(ctx context.Context, tokenHash [32]byte, now time.Time) (auth.Record, error) {
	res, err := consumeScript.Run(ctx, s.c, []string{refreshKey(tokenHash)}, now.Unix()).Result()
	if err != nil {
		return auth.Record{}, errs.Wrap(err, errs.KindUnavailable, auth.CodeStoreUnavailable, "redis consume failed")
	}
	arr, ok := res.([]any)
	if !ok || len(arr) == 0 {
		return auth.Record{}, errs.Internalf("consume_bad_reply", "unexpected reply: %v", res)
	}
	switch arr[0] {
	case "missing":
		return auth.Record{}, errs.Unauthorized(auth.CodeRefreshInvalid, "refresh token unknown")
	case "reused":
		return auth.Record{}, errs.Unauthorized(auth.CodeRefreshReused, "refresh token reused")
	case "expired":
		return auth.Record{}, errs.Unauthorized(auth.CodeRefreshExpired, "refresh token expired")
	case "ok":
		var r auth.Record
		r.TokenHash = tokenHash
		r.FamilyID, _ = arr[1].(string)
		if ph, _ := arr[2].(string); ph != "" {
			b, _ := hex.DecodeString(ph)
			copy(r.ParentHash[:], b)
		}
		r.Subject, _ = arr[3].(string)
		if v, ok := arr[4].(string); ok {
			n := atoi64(v)
			r.IssuedAt = time.Unix(n, 0).UTC()
		}
		r.UserAgent, _ = arr[5].(string)
		r.IP, _ = arr[6].(string)
		if v, ok := arr[7].(string); ok {
			n := atoi64(v)
			r.ExpiresAt = time.Unix(n, 0).UTC()
		}
		return r, nil
	default:
		return auth.Record{}, errs.Internalf("consume_bad_reply", "unknown tag: %v", arr[0])
	}
}

func atoi64(s string) int64 {
	var n int64
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int64(c-'0')
	}
	return n
}

// RevokeFamily / RevokeSubject / GarbageCollect — Task 24.

func (s *Store) RevokeFamily(ctx context.Context, familyID string) error {
	members, err := s.c.SMembers(ctx, familyKey(familyID)).Result()
	if err != nil {
		return errs.Wrap(err, errs.KindUnavailable, auth.CodeStoreUnavailable, "redis family lookup failed")
	}
	if len(members) == 0 {
		return nil
	}
	pipe := s.c.TxPipeline()
	for _, m := range members {
		pipe.HSet(ctx, "refresh:"+m, "revoked", "1")
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return errs.Wrap(err, errs.KindUnavailable, auth.CodeStoreUnavailable, "redis family revoke failed")
	}
	return nil
}

func (s *Store) RevokeSubject(ctx context.Context, subject string) error {
	members, err := s.c.SMembers(ctx, subjectKey(subject)).Result()
	if err != nil {
		return errs.Wrap(err, errs.KindUnavailable, auth.CodeStoreUnavailable, "redis subject lookup failed")
	}
	if len(members) == 0 {
		return nil
	}
	pipe := s.c.TxPipeline()
	for _, m := range members {
		pipe.HSet(ctx, "refresh:"+m, "revoked", "1")
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return errs.Wrap(err, errs.KindUnavailable, auth.CodeStoreUnavailable, "redis subject revoke failed")
	}
	return nil
}

// GarbageCollect is a best-effort sweeper for stale entries in family/subject
// sets. The records themselves are already removed by Redis EXPIREAT; this
// method just trims dangling set members. Returns the number of trimmed members.
func (s *Store) GarbageCollect(ctx context.Context, now time.Time) (int64, error) {
	var removed int64
	// SCAN through all refresh:family:* and refresh:subject:* sets, drop members
	// whose hash key no longer EXISTS.
	for _, pattern := range []string{"refresh:family:*", "refresh:subject:*"} {
		iter := s.c.Scan(ctx, 0, pattern, 100).Iterator()
		for iter.Next(ctx) {
			setKey := iter.Val()
			members, err := s.c.SMembers(ctx, setKey).Result()
			if err != nil {
				return removed, errs.Wrap(err, errs.KindUnavailable, auth.CodeStoreUnavailable, "redis gc smembers")
			}
			pipe := s.c.TxPipeline()
			for _, m := range members {
				if cnt, _ := s.c.Exists(ctx, "refresh:"+m).Result(); cnt == 0 {
					pipe.SRem(ctx, setKey, m)
					removed++
				}
			}
			if _, err := pipe.Exec(ctx); err != nil {
				return removed, errs.Wrap(err, errs.KindUnavailable, auth.CodeStoreUnavailable, "redis gc srem")
			}
		}
		if err := iter.Err(); err != nil {
			return removed, errs.Wrap(err, errs.KindUnavailable, auth.CodeStoreUnavailable, "redis gc scan")
		}
	}
	return removed, nil
}

// Compile-time interface assertion.
var _ auth.RefreshStore = (*Store)(nil)
