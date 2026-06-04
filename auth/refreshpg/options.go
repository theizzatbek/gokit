package refreshpg

import (
	"context"
	"log/slog"

	"github.com/prometheus/client_golang/prometheus"
)

// Option tunes the constructed *Store. The variadic shape on New keeps
// the zero-option call site (`refreshpg.New(db)`) compatible.
type Option func(*storeOpts)

type storeOpts struct {
	logger  *slog.Logger
	metrics prometheus.Registerer

	onConsumeReused ConsumeReusedHook
	onFamilyRevoke  FamilyRevokeHook
	onSubjectRevoke SubjectRevokeHook
	onIPRevoke      IPRevokeHook
}

// ConsumeReusedHook fires INSIDE Consume when the kit detects a refresh
// token that has already been consumed or revoked — the OAuth 2.1
// reuse-detection signal. RevokeFamily runs FIRST (the security-critical
// side effect happens regardless of hook behaviour); the hook only sees
// the event after the family has already been wiped.
//
// Wire it to your SIEM / Sentry / pager — a non-zero rate is the
// canonical "stolen refresh token" alert. Panic-safe.
type ConsumeReusedHook func(ctx context.Context, familyID, subject string)

// FamilyRevokeHook fires after a successful RevokeFamily. `count` is the
// number of rows revoked (0 = idempotent no-op). Panic-safe.
type FamilyRevokeHook func(ctx context.Context, familyID string, count int64)

// SubjectRevokeHook fires after a successful RevokeSubject. Panic-safe.
type SubjectRevokeHook func(ctx context.Context, subject string, count int64)

// IPRevokeHook fires after a successful RevokeByIP. Panic-safe. Use for
// incident-response audit trails.
type IPRevokeHook func(ctx context.Context, ip string, count int64)

// WithLogger wires a slog logger. Used for hook panic recovery and
// (sparingly) for diagnostic warnings — store ops are otherwise silent.
// nil = silent.
func WithLogger(l *slog.Logger) Option { return func(o *storeOpts) { o.logger = l } }

// WithMetrics enables Prometheus instrumentation. The store registers:
//
//   - refreshpg_ops_total{op,outcome}        — Issue / Consume /
//     RevokeFamily / RevokeSubject / RevokeByIP / GarbageCollect /
//     Stats / ListBySubject; outcome=ok|error (consume also: missing|
//     expired|reused)
//   - refreshpg_op_duration_seconds{op}      — wall-clock latency
//
// Pass the same Registerer you give to other kit subsystems for a
// single /metrics scrape.
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
