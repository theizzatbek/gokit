// Package refreshredis implements auth.RefreshStore on top of Redis.
// Each refresh record lives in one HASH; family + subject sets give the
// O(1) bulk-revoke paths. All TTLs are managed via EXPIREAT.
package refreshredis

import (
	"context"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/theizzatbek/gokit/auth"
	"github.com/theizzatbek/gokit/errs"
)

// Store is a Redis-backed RefreshStore. Client ownership stays with the
// caller. Observability + lifecycle hooks are opt-in via [Option].
type Store struct {
	c       *redis.Client
	logger  *slog.Logger
	metrics *metrics

	onConsumeReused ConsumeReusedHook
	onFamilyRevoke  FamilyRevokeHook
	onSubjectRevoke SubjectRevokeHook
	onIPRevoke      IPRevokeHook
}

// New wraps an existing *redis.Client. Trailing options enable metrics
// / logging / hooks; the zero-option form is unchanged from earlier
// versions.
func New(c *redis.Client, opts ...Option) *Store {
	o := storeOpts{}
	for _, fn := range opts {
		fn(&o)
	}
	s := &Store{
		c:               c,
		logger:          o.logger,
		onConsumeReused: o.onConsumeReused,
		onFamilyRevoke:  o.onFamilyRevoke,
		onSubjectRevoke: o.onSubjectRevoke,
		onIPRevoke:      o.onIPRevoke,
	}
	if o.metrics != nil {
		s.metrics = newMetrics(o.metrics)
	}
	return s
}

func refreshKey(h [32]byte) string { return "refresh:" + hashToHex(h) }
func familyKey(id string) string   { return "refresh:family:" + id }
func subjectKey(s string) string   { return "refresh:subject:" + s }
func ipKey(ip string) string       { return "refresh:ip:" + ip }
func hashToHex(h [32]byte) string  { return hex.EncodeToString(h[:]) }

// Issue creates the hash + family/subject/ip set entries with EXPIREAT.
//
// The auxiliary `refresh:ip:{ip}` set backs [Store.RevokeByIP]; it is
// populated only when `r.IP != ""` (anonymous-IP records stay outside
// the index so RevokeByIP("") is a deterministic no-op).
func (s *Store) Issue(ctx context.Context, r auth.Record) error {
	if r.ExpiresAt.IsZero() {
		return errs.Validation("missing_expiry", "Record.ExpiresAt required")
	}
	start := time.Now()
	defer func() { s.metrics.observe("issue", time.Since(start).Seconds()) }()

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
	if r.IP != "" {
		pipe.SAdd(ctx, ipKey(r.IP), hashToHex(r.TokenHash))
		pipe.ExpireAt(ctx, ipKey(r.IP), r.ExpiresAt.Add(time.Hour))
	}
	if _, err := pipe.Exec(ctx); err != nil {
		s.metrics.inc("issue", "error")
		return errs.Wrap(err, errs.KindUnavailable, auth.CodeStoreUnavailable, "redis refresh issue failed")
	}
	s.metrics.inc("issue", "ok")
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
	start := time.Now()
	defer func() { s.metrics.observe("consume", time.Since(start).Seconds()) }()

	res, err := consumeScript.Run(ctx, s.c, []string{refreshKey(tokenHash)}, now.Unix()).Result()
	if err != nil {
		s.metrics.inc("consume", "error")
		return auth.Record{}, errs.Wrap(err, errs.KindUnavailable, auth.CodeStoreUnavailable, "redis consume failed")
	}
	arr, ok := res.([]any)
	if !ok || len(arr) == 0 {
		s.metrics.inc("consume", "error")
		return auth.Record{}, errs.Internalf("consume_bad_reply", "unexpected reply: %v", res)
	}
	switch arr[0] {
	case "missing":
		s.metrics.inc("consume", "missing")
		return auth.Record{}, errs.Unauthorized(auth.CodeRefreshInvalid, "refresh token unknown")
	case "reused":
		s.metrics.inc("consume", "reused")
		// The Lua script already revoked every family member; we still
		// need the family/subject for the hook payload — best-effort
		// HMGET so a partially-evicted hash doesn't fail the path.
		var familyID, subject string
		if vals, hErr := s.c.HMGet(ctx, refreshKey(tokenHash), "family", "subject").Result(); hErr == nil && len(vals) == 2 {
			if v, _ := vals[0].(string); v != "" {
				familyID = v
			}
			if v, _ := vals[1].(string); v != "" {
				subject = v
			}
		}
		s.fireConsumeReused(ctx, familyID, subject)
		return auth.Record{}, errs.Unauthorized(auth.CodeRefreshReused, "refresh token reused")
	case "expired":
		s.metrics.inc("consume", "expired")
		return auth.Record{}, errs.Unauthorized(auth.CodeRefreshExpired, "refresh token expired")
	case "ok":
		s.metrics.inc("consume", "ok")
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
		s.metrics.inc("consume", "error")
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

// RevokeFamily marks every member of the family set revoked.
func (s *Store) RevokeFamily(ctx context.Context, familyID string) error {
	start := time.Now()
	defer func() { s.metrics.observe("revoke_family", time.Since(start).Seconds()) }()

	members, err := s.c.SMembers(ctx, familyKey(familyID)).Result()
	if err != nil {
		s.metrics.inc("revoke_family", "error")
		return errs.Wrap(err, errs.KindUnavailable, auth.CodeStoreUnavailable, "redis family lookup failed")
	}
	if len(members) == 0 {
		s.metrics.inc("revoke_family", "ok")
		s.fireFamilyRevoke(ctx, familyID, 0)
		return nil
	}
	pipe := s.c.TxPipeline()
	for _, m := range members {
		pipe.HSet(ctx, "refresh:"+m, "revoked", "1")
	}
	if _, err := pipe.Exec(ctx); err != nil {
		s.metrics.inc("revoke_family", "error")
		return errs.Wrap(err, errs.KindUnavailable, auth.CodeStoreUnavailable, "redis family revoke failed")
	}
	s.metrics.inc("revoke_family", "ok")
	s.fireFamilyRevoke(ctx, familyID, int64(len(members)))
	return nil
}

// RevokeSubject marks every member of the subject set revoked.
func (s *Store) RevokeSubject(ctx context.Context, subject string) error {
	start := time.Now()
	defer func() { s.metrics.observe("revoke_subject", time.Since(start).Seconds()) }()

	members, err := s.c.SMembers(ctx, subjectKey(subject)).Result()
	if err != nil {
		s.metrics.inc("revoke_subject", "error")
		return errs.Wrap(err, errs.KindUnavailable, auth.CodeStoreUnavailable, "redis subject lookup failed")
	}
	if len(members) == 0 {
		s.metrics.inc("revoke_subject", "ok")
		s.fireSubjectRevoke(ctx, subject, 0)
		return nil
	}
	pipe := s.c.TxPipeline()
	for _, m := range members {
		pipe.HSet(ctx, "refresh:"+m, "revoked", "1")
	}
	if _, err := pipe.Exec(ctx); err != nil {
		s.metrics.inc("revoke_subject", "error")
		return errs.Wrap(err, errs.KindUnavailable, auth.CodeStoreUnavailable, "redis subject revoke failed")
	}
	s.metrics.inc("revoke_subject", "ok")
	s.fireSubjectRevoke(ctx, subject, int64(len(members)))
	return nil
}

// RevokeByIP marks every refresh-token issued from the supplied IP as
// revoked. Backed by the `refresh:ip:{ip}` auxiliary set populated at
// [Store.Issue] time when `r.IP != ""`. Tokens issued before this
// feature shipped do NOT appear in the index — for retroactive sweep,
// run [Store.Stats]/[Store.ListBySubject] from operator scripts.
//
// Returns the number of token records marked revoked. Empty `ip`
// returns 0 without touching Redis.
func (s *Store) RevokeByIP(ctx context.Context, ip string) (int64, error) {
	if ip == "" {
		return 0, nil
	}
	start := time.Now()
	defer func() { s.metrics.observe("revoke_ip", time.Since(start).Seconds()) }()

	members, err := s.c.SMembers(ctx, ipKey(ip)).Result()
	if err != nil {
		s.metrics.inc("revoke_ip", "error")
		return 0, errs.Wrap(err, errs.KindUnavailable, auth.CodeStoreUnavailable, "redis ip lookup failed")
	}
	if len(members) == 0 {
		s.metrics.inc("revoke_ip", "ok")
		s.fireIPRevoke(ctx, ip, 0)
		return 0, nil
	}
	pipe := s.c.TxPipeline()
	for _, m := range members {
		pipe.HSet(ctx, "refresh:"+m, "revoked", "1")
	}
	if _, err := pipe.Exec(ctx); err != nil {
		s.metrics.inc("revoke_ip", "error")
		return 0, errs.Wrap(err, errs.KindUnavailable, auth.CodeStoreUnavailable, "redis ip revoke failed")
	}
	s.metrics.inc("revoke_ip", "ok")
	n := int64(len(members))
	s.fireIPRevoke(ctx, ip, n)
	return n, nil
}

// GarbageCollect is a best-effort sweeper for stale entries in
// family/subject/ip sets. The records themselves are already removed by
// Redis EXPIREAT; this method just trims dangling set members. Returns
// the number of trimmed members.
func (s *Store) GarbageCollect(ctx context.Context, _ time.Time) (int64, error) {
	start := time.Now()
	defer func() { s.metrics.observe("gc", time.Since(start).Seconds()) }()

	var removed int64
	// SCAN through all refresh:family:* / refresh:subject:* / refresh:ip:* sets,
	// drop members whose hash key no longer EXISTS.
	for _, pattern := range []string{"refresh:family:*", "refresh:subject:*", "refresh:ip:*"} {
		iter := s.c.Scan(ctx, 0, pattern, 100).Iterator()
		for iter.Next(ctx) {
			setKey := iter.Val()
			members, err := s.c.SMembers(ctx, setKey).Result()
			if err != nil {
				s.metrics.inc("gc", "error")
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
				s.metrics.inc("gc", "error")
				return removed, errs.Wrap(err, errs.KindUnavailable, auth.CodeStoreUnavailable, "redis gc srem")
			}
		}
		if err := iter.Err(); err != nil {
			s.metrics.inc("gc", "error")
			return removed, errs.Wrap(err, errs.KindUnavailable, auth.CodeStoreUnavailable, "redis gc scan")
		}
	}
	s.metrics.inc("gc", "ok")
	return removed, nil
}

// SessionInfo is the admin-side projection of a stored refresh-token
// hash. NEVER includes `token_hash` itself — the hash is secret
// material and stays in the store.
type SessionInfo struct {
	FamilyID   string
	Subject    string
	IssuedAt   time.Time
	ExpiresAt  time.Time
	ConsumedAt time.Time // zero = still live (consumed=0)
	RevokedAt  time.Time // zero = active (revoked=0)
	UserAgent  string
	IP         string
	State      string // "active" | "consumed" | "revoked" | "expired"
}

// ListBySubject returns every refresh-token record bound to the subject
// ordered by `issued_at DESC`. Backed by the `refresh:subject:{subject}`
// set + per-hash HGetAll.
//
// Empty subject returns an empty slice without touching Redis. Members
// of the index set whose backing hash has already been EXPIREATd are
// silently skipped (they would be SREM'd by [Store.GarbageCollect]).
func (s *Store) ListBySubject(ctx context.Context, subject string) ([]SessionInfo, error) {
	if subject == "" {
		return []SessionInfo{}, nil
	}
	start := time.Now()
	defer func() { s.metrics.observe("list", time.Since(start).Seconds()) }()

	members, err := s.c.SMembers(ctx, subjectKey(subject)).Result()
	if err != nil {
		s.metrics.inc("list", "error")
		return nil, errs.Wrap(err, errs.KindUnavailable, auth.CodeStoreUnavailable, "redis list smembers")
	}
	if len(members) == 0 {
		s.metrics.inc("list", "ok")
		return []SessionInfo{}, nil
	}
	pipe := s.c.Pipeline()
	cmds := make([]*redis.MapStringStringCmd, len(members))
	for i, m := range members {
		cmds[i] = pipe.HGetAll(ctx, "refresh:"+m)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		s.metrics.inc("list", "error")
		return nil, errs.Wrap(err, errs.KindUnavailable, auth.CodeStoreUnavailable, "redis list hgetall")
	}
	now := time.Now()
	out := make([]SessionInfo, 0, len(members))
	for _, cmd := range cmds {
		fields := cmd.Val()
		if len(fields) == 0 {
			continue // hash EXPIREATd, set member is stale
		}
		out = append(out, hashToSession(fields, now))
	}
	// Sort issued_at DESC. len(out) is bounded by the subject set size —
	// a sort.Slice is fine.
	sortSessionsDesc(out)
	s.metrics.inc("list", "ok")
	return out, nil
}

// StoreStats is the rollup returned by [Store.Stats]. Buckets are
// disjoint per [sessionState]: Active + Consumed + Revoked + Expired =
// Total. Tokens whose backing hash has already been EXPIREATd are
// invisible to Stats — Redis does not keep them around.
type StoreStats struct {
	Active   int
	Consumed int
	Revoked  int
	Expired  int
	Total    int
}

// Stats walks `refresh:*` keys (excluding aux sets) via SCAN +
// pipelined HMGET to compute the disjoint-bucket rollup. O(N) — for
// admin / diagnostic use, not a hot path.
//
// Tip: pair with a Prometheus scrape that pulls Stats every 60s into
// `refreshredis_records{state}` Gauges if you need an alertable view.
func (s *Store) Stats(ctx context.Context) (StoreStats, error) {
	start := time.Now()
	defer func() { s.metrics.observe("stats", time.Since(start).Seconds()) }()

	var out StoreStats
	now := time.Now().Unix()
	const batchSize = 200

	iter := s.c.Scan(ctx, 0, "refresh:*", batchSize).Iterator()
	keys := make([]string, 0, batchSize)
	flush := func() error {
		if len(keys) == 0 {
			return nil
		}
		pipe := s.c.Pipeline()
		cmds := make([]*redis.SliceCmd, len(keys))
		for i, k := range keys {
			cmds[i] = pipe.HMGet(ctx, k, "consumed", "revoked", "expires_at")
		}
		if _, err := pipe.Exec(ctx); err != nil {
			return err
		}
		for _, cmd := range cmds {
			vals, _ := cmd.Result()
			if len(vals) != 3 || vals[0] == nil {
				// Not a refresh-record hash (aux set / unrelated key
				// with same prefix). Skip.
				continue
			}
			consumed, _ := vals[0].(string)
			revoked, _ := vals[1].(string)
			expStr, _ := vals[2].(string)
			out.Total++
			exp := atoi64(expStr)
			switch {
			case revoked == "1":
				out.Revoked++
			case consumed == "1":
				out.Consumed++
			case exp <= now:
				out.Expired++
			default:
				out.Active++
			}
		}
		keys = keys[:0]
		return nil
	}
	for iter.Next(ctx) {
		k := iter.Val()
		// Skip the aux index sets — they share the "refresh:" prefix.
		if strings.HasPrefix(k, "refresh:family:") ||
			strings.HasPrefix(k, "refresh:subject:") ||
			strings.HasPrefix(k, "refresh:ip:") {
			continue
		}
		keys = append(keys, k)
		if len(keys) >= batchSize {
			if err := flush(); err != nil {
				s.metrics.inc("stats", "error")
				return StoreStats{}, errs.Wrap(err, errs.KindUnavailable, auth.CodeStoreUnavailable, "redis stats hmget")
			}
		}
	}
	if err := iter.Err(); err != nil {
		s.metrics.inc("stats", "error")
		return StoreStats{}, errs.Wrap(err, errs.KindUnavailable, auth.CodeStoreUnavailable, "redis stats scan")
	}
	if err := flush(); err != nil {
		s.metrics.inc("stats", "error")
		return StoreStats{}, errs.Wrap(err, errs.KindUnavailable, auth.CodeStoreUnavailable, "redis stats hmget")
	}
	s.metrics.inc("stats", "ok")
	return out, nil
}

// hashToSession decodes a `refresh:<hash>` HGetAll into a SessionInfo
// + computed State. `fields` is the verbatim map returned by go-redis.
func hashToSession(fields map[string]string, now time.Time) SessionInfo {
	info := SessionInfo{
		FamilyID:  fields["family"],
		Subject:   fields["subject"],
		UserAgent: fields["user_agent"],
		IP:        fields["ip"],
	}
	if v := fields["issued_at"]; v != "" {
		info.IssuedAt = time.Unix(atoi64(v), 0).UTC()
	}
	if v := fields["expires_at"]; v != "" {
		info.ExpiresAt = time.Unix(atoi64(v), 0).UTC()
	}
	// We don't persist consumed_at / revoked_at separately — only the
	// boolean flags. Use the ConsumedAt/RevokedAt fields as a "set"
	// indicator: zero = false, non-zero = true (with `now` as the
	// placeholder timestamp). This matches the SessionInfo contract
	// the admin layer documents.
	if fields["consumed"] == "1" {
		info.ConsumedAt = now
	}
	if fields["revoked"] == "1" {
		info.RevokedAt = now
	}
	info.State = sessionState(info, now)
	return info
}

// sessionState classifies a SessionInfo. Revoke wins over consume;
// consume wins over expiry; expiry wins over active. Buckets are
// disjoint.
func sessionState(info SessionInfo, now time.Time) string {
	switch {
	case !info.RevokedAt.IsZero():
		return "revoked"
	case !info.ConsumedAt.IsZero():
		return "consumed"
	case info.ExpiresAt.Before(now) || info.ExpiresAt.Equal(now):
		return "expired"
	default:
		return "active"
	}
}

// sortSessionsDesc sorts in place by IssuedAt descending. Inlined to
// avoid importing sort just for one call site.
func sortSessionsDesc(s []SessionInfo) {
	// Insertion sort is fine for session lists (typically < 50 entries
	// per subject); avoids a sort.Slice closure allocation on the hot
	// admin path.
	for i := 1; i < len(s); i++ {
		j := i
		for j > 0 && s[j].IssuedAt.After(s[j-1].IssuedAt) {
			s[j], s[j-1] = s[j-1], s[j]
			j--
		}
	}
}

// fireConsumeReused invokes the OnConsumeReused hook under a recover.
func (s *Store) fireConsumeReused(ctx context.Context, familyID, subject string) {
	if s.onConsumeReused == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil && s.logger != nil {
			s.logger.WarnContext(ctx, "refreshredis: OnConsumeReused panic recovered",
				"panic", fmt.Sprint(r), "family_id", familyID)
		}
	}()
	s.onConsumeReused(ctx, familyID, subject)
}

// fireFamilyRevoke invokes the OnFamilyRevoke hook under a recover.
func (s *Store) fireFamilyRevoke(ctx context.Context, familyID string, count int64) {
	if s.onFamilyRevoke == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil && s.logger != nil {
			s.logger.WarnContext(ctx, "refreshredis: OnFamilyRevoke panic recovered",
				"panic", fmt.Sprint(r), "family_id", familyID)
		}
	}()
	s.onFamilyRevoke(ctx, familyID, count)
}

// fireSubjectRevoke invokes the OnSubjectRevoke hook under a recover.
func (s *Store) fireSubjectRevoke(ctx context.Context, subject string, count int64) {
	if s.onSubjectRevoke == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil && s.logger != nil {
			s.logger.WarnContext(ctx, "refreshredis: OnSubjectRevoke panic recovered",
				"panic", fmt.Sprint(r), "subject", subject)
		}
	}()
	s.onSubjectRevoke(ctx, subject, count)
}

// fireIPRevoke invokes the OnIPRevoke hook under a recover.
func (s *Store) fireIPRevoke(ctx context.Context, ip string, count int64) {
	if s.onIPRevoke == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil && s.logger != nil {
			s.logger.WarnContext(ctx, "refreshredis: OnIPRevoke panic recovered",
				"panic", fmt.Sprint(r), "ip", ip)
		}
	}()
	s.onIPRevoke(ctx, ip, count)
}

// Compile-time interface assertion.
var _ auth.RefreshStore = (*Store)(nil)
