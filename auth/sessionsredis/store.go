// Package sessionsredis implements auth/sessions.SessionStore on top
// of Redis. Each session is one HASH with EXPIREAT; a subject SET
// indexes sessions per user so DeleteForSubject is O(N) over the
// user's own sessions, not the whole keyspace.
package sessionsredis

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/theizzatbek/gokit/auth/sessions"
	xerrs "github.com/theizzatbek/gokit/errs"
)

// Stable error Code constants.
const (
	// CodeRedisFailed — Redis returned a transport error.
	CodeRedisFailed = "sessionsredis_transport"
)

// Store is a Redis-backed [sessions.SessionStore]. Client ownership
// stays with the caller (typically svc.Redis.Redis()).
type Store struct {
	c      *redis.Client
	prefix string
}

// New wraps an existing *redis.Client. prefix namespaces every key
// under "<prefix>session:" / "<prefix>session:subject:" so the same
// Redis instance can host multiple services (or multiple realms in
// one service) without collision. Pass e.g. "app1:" or "tenant-a:".
func New(c *redis.Client, prefix string) *Store {
	return &Store{c: c, prefix: prefix}
}

func (s *Store) sessKey(id string) string { return s.prefix + "session:" + id }
func (s *Store) subjKey(sub string) string {
	return s.prefix + "session:subject:" + sub
}

// Create stores the session as a HASH and adds its ID to the
// subject's SET. Both keys share the same EXPIREAT for clean GC.
func (s *Store) Create(ctx context.Context, sess *sessions.Session) error {
	if sess.ExpiresAt.IsZero() {
		return xerrs.Validation(CodeRedisFailed, "sessions: ExpiresAt required")
	}
	pipe := s.c.TxPipeline()
	pipe.HSet(ctx, s.sessKey(sess.ID), map[string]any{
		"subject": sess.Subject,
		// go-redis expects []byte / string / numeric — cast
		// json.RawMessage so it doesn't hit the no-binary-marshaler
		// path.
		"claims":       string(sess.Claims),
		"scopes":       encodeStrSlice(sess.Scopes),
		"roles":        encodeStrSlice(sess.Roles),
		"created_at":   sess.CreatedAt.Unix(),
		"last_seen_at": sess.LastSeenAt.Unix(),
		"expires_at":   sess.ExpiresAt.Unix(),
	})
	pipe.ExpireAt(ctx, s.sessKey(sess.ID), sess.ExpiresAt)
	pipe.SAdd(ctx, s.subjKey(sess.Subject), sess.ID)
	pipe.ExpireAt(ctx, s.subjKey(sess.Subject), sess.ExpiresAt)
	if _, err := pipe.Exec(ctx); err != nil {
		return xerrs.Wrap(err, xerrs.KindUnavailable, CodeRedisFailed,
			"sessionsredis: create failed")
	}
	return nil
}

// Get loads the HASH; missing key returns (nil, nil) per the
// SessionStore contract.
func (s *Store) Get(ctx context.Context, id string) (*sessions.Session, error) {
	res, err := s.c.HGetAll(ctx, s.sessKey(id)).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, xerrs.Wrap(err, xerrs.KindUnavailable, CodeRedisFailed,
			"sessionsredis: get failed")
	}
	if len(res) == 0 {
		return nil, nil
	}
	out := &sessions.Session{
		ID:         id,
		Subject:    res["subject"],
		Claims:     []byte(res["claims"]),
		Scopes:     decodeStrSlice(res["scopes"]),
		Roles:      decodeStrSlice(res["roles"]),
		CreatedAt:  parseUnix(res["created_at"]),
		LastSeenAt: parseUnix(res["last_seen_at"]),
		ExpiresAt:  parseUnix(res["expires_at"]),
	}
	return out, nil
}

// Touch advances LastSeenAt + ExpiresAt and re-arms EXPIREAT on
// both the session and subject keys (sliding refresh).
func (s *Store) Touch(ctx context.Context, id string, lastSeen, expires time.Time) error {
	subject, err := s.c.HGet(ctx, s.sessKey(id), "subject").Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil
		}
		return xerrs.Wrap(err, xerrs.KindUnavailable, CodeRedisFailed,
			"sessionsredis: touch lookup failed")
	}
	pipe := s.c.TxPipeline()
	pipe.HSet(ctx, s.sessKey(id),
		"last_seen_at", lastSeen.Unix(),
		"expires_at", expires.Unix())
	pipe.ExpireAt(ctx, s.sessKey(id), expires)
	if subject != "" {
		pipe.ExpireAt(ctx, s.subjKey(subject), expires)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return xerrs.Wrap(err, xerrs.KindUnavailable, CodeRedisFailed,
			"sessionsredis: touch failed")
	}
	return nil
}

// Delete removes the HASH and removes the ID from the subject's SET.
func (s *Store) Delete(ctx context.Context, id string) error {
	subject, _ := s.c.HGet(ctx, s.sessKey(id), "subject").Result()
	pipe := s.c.TxPipeline()
	pipe.Del(ctx, s.sessKey(id))
	if subject != "" {
		pipe.SRem(ctx, s.subjKey(subject), id)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return xerrs.Wrap(err, xerrs.KindUnavailable, CodeRedisFailed,
			"sessionsredis: delete failed")
	}
	return nil
}

// DeleteForSubject deletes every session in the subject's SET, then
// the SET itself.
func (s *Store) DeleteForSubject(ctx context.Context, subject string) error {
	ids, err := s.c.SMembers(ctx, s.subjKey(subject)).Result()
	if err != nil {
		return xerrs.Wrap(err, xerrs.KindUnavailable, CodeRedisFailed,
			"sessionsredis: bulk delete enumerate failed")
	}
	if len(ids) == 0 {
		return nil
	}
	pipe := s.c.TxPipeline()
	for _, id := range ids {
		pipe.Del(ctx, s.sessKey(id))
	}
	pipe.Del(ctx, s.subjKey(subject))
	if _, err := pipe.Exec(ctx); err != nil {
		return xerrs.Wrap(err, xerrs.KindUnavailable, CodeRedisFailed,
			"sessionsredis: bulk delete failed")
	}
	return nil
}

// encodeStrSlice JSON-encodes for HASH storage. Empty slice → empty
// string (HASH-friendly), differentiates from null.
func encodeStrSlice(in []string) string {
	if len(in) == 0 {
		return ""
	}
	raw, _ := json.Marshal(in)
	return string(raw)
}

func decodeStrSlice(raw string) []string {
	if raw == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

func parseUnix(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	var sec int64
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return time.Time{}
		}
		sec = sec*10 + int64(c-'0')
	}
	return time.Unix(sec, 0).UTC()
}
