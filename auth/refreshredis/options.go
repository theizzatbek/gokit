package refreshredis

import (
	"context"
	"log/slog"

	"github.com/prometheus/client_golang/prometheus"
)

// Option tunes the constructed *Store. The variadic shape on New keeps
// the zero-option call site (`refreshredis.New(rdb)`) compatible.
type Option func(*storeOpts)

type storeOpts struct {
	logger  *slog.Logger
	metrics prometheus.Registerer

	onConsumeReused ConsumeReusedHook
	onFamilyRevoke  FamilyRevokeHook
	onSubjectRevoke SubjectRevokeHook
	onIPRevoke      IPRevokeHook

	statsCap int // 0 = unbounded; see WithStatsCap.
}

// ConsumeReusedHook fires INSIDE Consume on a refresh reuse-detection
// event (OAuth 2.1 stolen-token signal). The Lua script revokes every
// family member FIRST; the hook only sees the event after the side
// effect has already completed.
//
// Wire it to your SIEM / Sentry / pager — a non-zero rate is the
// canonical alert. Panic-safe.
type ConsumeReusedHook func(ctx context.Context, familyID, subject string)

// FamilyRevokeHook fires after a successful RevokeFamily. `count` is
// the number of tokens revoked (0 = idempotent no-op when the family
// set is empty). Panic-safe.
type FamilyRevokeHook func(ctx context.Context, familyID string, count int64)

// SubjectRevokeHook fires after a successful RevokeSubject. Panic-safe.
type SubjectRevokeHook func(ctx context.Context, subject string, count int64)

// IPRevokeHook fires after a successful RevokeByIP. Panic-safe. Use for
// incident-response audit trails.
type IPRevokeHook func(ctx context.Context, ip string, count int64)

// WithLogger wires a slog logger. Used for hook panic recovery and
// (sparingly) diagnostic warnings — store ops are otherwise silent.
// nil = silent.
func WithLogger(l *slog.Logger) Option { return func(o *storeOpts) { o.logger = l } }

// WithMetrics enables Prometheus instrumentation. The store registers:
//
//   - refreshredis_ops_total{op,outcome}      — issue / consume /
//     revoke_family / revoke_subject / revoke_ip / gc / stats / list;
//     outcome=ok|error (consume also: missing|expired|reused)
//   - refreshredis_op_duration_seconds{op}    — wall-clock latency
//
// Pass the same Registerer you give to other kit subsystems.
func WithMetrics(reg prometheus.Registerer) Option {
	return func(o *storeOpts) { o.metrics = reg }
}

// WithOnConsumeReused registers a callback fired on every refresh
// reuse-detection event. See [ConsumeReusedHook]. Multiple calls — last
// wins.
func WithOnConsumeReused(fn ConsumeReusedHook) Option {
	return func(o *storeOpts) { o.onConsumeReused = fn }
}

// WithOnFamilyRevoke registers a post-RevokeFamily callback. See
// [FamilyRevokeHook]. Multiple calls — last wins.
func WithOnFamilyRevoke(fn FamilyRevokeHook) Option {
	return func(o *storeOpts) { o.onFamilyRevoke = fn }
}

// WithOnSubjectRevoke registers a post-RevokeSubject callback. See
// [SubjectRevokeHook]. Multiple calls — last wins.
func WithOnSubjectRevoke(fn SubjectRevokeHook) Option {
	return func(o *storeOpts) { o.onSubjectRevoke = fn }
}

// WithOnIPRevoke registers a post-RevokeByIP callback. See
// [IPRevokeHook]. Multiple calls — last wins.
func WithOnIPRevoke(fn IPRevokeHook) Option {
	return func(o *storeOpts) { o.onIPRevoke = fn }
}

// WithStatsCap bounds the number of keys [Store.Stats] is willing to
// SCAN before returning [ErrStatsCapExceeded]. 0 (default) leaves
// Stats unbounded — fine for diagnostic shells but a foot-gun in
// long-running /admin endpoints when the keyspace grows unexpectedly.
//
// When the cap is hit, the partially accumulated counts are
// discarded and the error returned with the configured cap embedded
// in its message — callers should retry against a smaller subset
// (e.g. [Store.ListBySubject]) or raise the cap intentionally.
//
// Negative values are clamped to 0.
func WithStatsCap(n int) Option {
	if n < 0 {
		n = 0
	}
	return func(o *storeOpts) { o.statsCap = n }
}
