package sessions

import (
	"context"
	"log/slog"

	"github.com/prometheus/client_golang/prometheus"
)

// ManagerOption tunes a [Manager] beyond what [Config] covers. The
// variadic shape on [NewManager] keeps the previous two-arg call site
// (`sessions.NewManager(cfg, setPrincipal)`) compatible.
type ManagerOption func(*managerOpts)

type managerOpts struct {
	logger  *slog.Logger
	metrics prometheus.Registerer

	onIssue            IssueHook
	onLogout           LogoutHook
	onLogoutEverywhere LogoutEverywhereHook
	onExpire           ExpireHook
}

// IssueHook fires after a successful [Manager.Issue] — the cookie has
// been written and the Session is persisted in the Store. Use for
// Sentry user-scope binding, audit logging, "welcome back" telemetry.
// Panic-safe; recovered panics surface as WARN via [WithLogger].
type IssueHook func(ctx context.Context, sess *Session)

// LogoutHook fires after a successful [Manager.Logout] (or
// [Manager.RevokeByID]) — the Store has dropped the row and the
// cookie has been cleared. `subject` may be empty when the operation
// short-circuited (no cookie was present).
type LogoutHook func(ctx context.Context, sessionID, subject string)

// LogoutEverywhereHook fires after a successful
// [Manager.LogoutEverywhere]. `count` is the best-effort count of
// sessions revoked (a Store that doesn't track it returns -1).
type LogoutEverywhereHook func(ctx context.Context, subject string, count int)

// ExpireHook fires when [Manager.Middleware] sees an expired session
// and deletes it in-line. Use for "session timeout" audit signals
// distinct from explicit logout. Panic-safe.
type ExpireHook func(ctx context.Context, sessionID, subject string)

// WithLogger wires a slog logger. Used for hook panic recovery and
// (sparingly) diagnostic warnings — Manager ops are otherwise silent.
// nil = silent.
func WithLogger(l *slog.Logger) ManagerOption {
	return func(o *managerOpts) { o.logger = l }
}

// WithMetrics enables Prometheus instrumentation. The Manager registers:
//
//   - sessions_ops_total{op,outcome}        — Issue / Logout /
//     LogoutEverywhere / Middleware; outcome = ok | error |
//     (middleware also: missing | invalid | expired | claims_decode)
//   - sessions_op_duration_seconds{op}      — wall-clock latency
//
// Pass the same Registerer you give to other kit subsystems for a
// single /metrics scrape.
func WithMetrics(reg prometheus.Registerer) ManagerOption {
	return func(o *managerOpts) { o.metrics = reg }
}

// WithOnIssue registers a post-issue callback. See [IssueHook]. Last
// call wins.
func WithOnIssue(fn IssueHook) ManagerOption {
	return func(o *managerOpts) { o.onIssue = fn }
}

// WithOnLogout registers a post-logout callback. See [LogoutHook].
// Last call wins.
func WithOnLogout(fn LogoutHook) ManagerOption {
	return func(o *managerOpts) { o.onLogout = fn }
}

// WithOnLogoutEverywhere registers a post-LogoutEverywhere callback.
// See [LogoutEverywhereHook]. Last call wins.
func WithOnLogoutEverywhere(fn LogoutEverywhereHook) ManagerOption {
	return func(o *managerOpts) { o.onLogoutEverywhere = fn }
}

// WithOnExpire registers an in-line expire callback. See [ExpireHook].
// Last call wins.
func WithOnExpire(fn ExpireHook) ManagerOption {
	return func(o *managerOpts) { o.onExpire = fn }
}
