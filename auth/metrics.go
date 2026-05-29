package auth

import "github.com/prometheus/client_golang/prometheus"

// authMetrics is the registered set of Prometheus collectors that
// instrument auth's flow: token issuance, bearer verification,
// refresh rotation, logout, and the rate-limit / idempotency
// middlewares.
//
// Constructed by [newAuthMetrics] only when [WithMetrics] is wired —
// otherwise the *Auth[C] holds a nil pointer and every increment
// helper is a no-op (see the nil-safe methods below). This keeps the
// hot path branch-free for callers that don't opt into metrics.
type authMetrics struct {
	tokensIssued    *prometheus.CounterVec // op=login|refresh
	tokenIssueFails *prometheus.CounterVec // op=login|refresh, reason=sign|store
	bearerVerify    *prometheus.CounterVec // outcome=ok|invalid
	refresh         *prometheus.CounterVec // outcome=ok|reused|expired|invalid|missing
	logout          *prometheus.CounterVec // scope=single|all
	rateLimitDenied prometheus.Counter
	idempotency     *prometheus.CounterVec // outcome=hit|miss|skip
}

// newAuthMetrics constructs and registers all auth collectors on reg.
// Panics with prometheus.AlreadyRegisteredError if a second *Auth[C]
// is built against the same Registerer — auth metrics are unique per
// registry. Callers that need multiple Auth instances on one process
// should pass distinct registries.
func newAuthMetrics(reg prometheus.Registerer) *authMetrics {
	m := &authMetrics{
		tokensIssued: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "auth",
			Name:      "tokens_issued_total",
			Help:      "Number of access+refresh token pairs issued, by op (login=initial issuance, refresh=rotation).",
		}, []string{"op"}),
		tokenIssueFails: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "auth",
			Name:      "token_issue_failed_total",
			Help:      "Failures encountered after credential verification while minting tokens, by op + failure reason.",
		}, []string{"op", "reason"}),
		bearerVerify: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "auth",
			Name:      "bearer_verify_total",
			Help:      "Outcome of the Bearer middleware's JWT verify step. invalid covers expired/malformed/signature-mismatch.",
		}, []string{"outcome"}),
		refresh: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "auth",
			Name:      "refresh_total",
			Help:      "RotateRefresh outcomes. outcome=reused is the OAuth-2.1 reuse-detection signal; alert on a non-zero rate.",
		}, []string{"outcome"}),
		logout: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "auth",
			Name:      "logout_total",
			Help:      "Logout calls. scope=single revokes the current refresh family; scope=all revokes every refresh for the subject.",
		}, []string{"scope"}),
		rateLimitDenied: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "auth",
			Name:      "ratelimit_denied_total",
			Help:      "Requests rejected by the auth.RateLimit / auth.RateLimitBySubject middlewares (429 with Retry-After).",
		}),
		idempotency: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "auth",
			Name:      "idempotency_total",
			Help:      "Idempotency middleware events. hit=cached replay; miss=first execution stored; skip=request had no Idempotency-Key or was a safe method.",
		}, []string{"outcome"}),
	}
	reg.MustRegister(
		m.tokensIssued,
		m.tokenIssueFails,
		m.bearerVerify,
		m.refresh,
		m.logout,
		m.rateLimitDenied,
		m.idempotency,
	)
	return m
}

func (m *authMetrics) incTokensIssued(op string) {
	if m == nil {
		return
	}
	m.tokensIssued.WithLabelValues(op).Inc()
}

func (m *authMetrics) incTokenIssueFailed(op, reason string) {
	if m == nil {
		return
	}
	m.tokenIssueFails.WithLabelValues(op, reason).Inc()
}

func (m *authMetrics) incBearerVerify(outcome string) {
	if m == nil {
		return
	}
	m.bearerVerify.WithLabelValues(outcome).Inc()
}

func (m *authMetrics) incRefresh(outcome string) {
	if m == nil {
		return
	}
	m.refresh.WithLabelValues(outcome).Inc()
}

func (m *authMetrics) incLogout(scope string) {
	if m == nil {
		return
	}
	m.logout.WithLabelValues(scope).Inc()
}

func (m *authMetrics) incRateLimitDenied() {
	if m == nil {
		return
	}
	m.rateLimitDenied.Inc()
}

func (m *authMetrics) incIdempotency(outcome string) {
	if m == nil {
		return
	}
	m.idempotency.WithLabelValues(outcome).Inc()
}