// Package runbook is a runtime kill-switch primitive. It lets
// operators flip named flags during an incident without a deploy
// — disable a broken feature, drain traffic from a faulty
// region, force-503 a misbehaving route. The kit reads the flags
// via fast in-memory cache; ops change them via a small auth-gated
// admin endpoint.
//
//	rb, _ := runbook.New(runbook.NewMemoryStore())
//
//	// In a handler:
//	if !rb.Enabled(ctx, "checkout") {
//	    return errs.Unavailable("checkout_disabled", "feature paused by ops")
//	}
//
//	// In your admin Fiber sub-router:
//	app.Use("/_kit/runbook", auth.Bearer(auth.BearerRequired),
//	    auth.RequireRole("ops"))
//	runbook.Mount(app, "/_kit/runbook", rb)
//
// Semantics:
//
//   - Flags are named strings keyed by flag-name. Boolean only —
//     either enabled or disabled. A flag without a stored value is
//     "enabled by default" (so a missing store entry never breaks a
//     feature; ops have to ACTIVELY disable it).
//   - SetEnabled(ctx, name, true/false) persists into Store; Enabled
//     reads from the in-process cache that refreshes on every Set
//     AND on a periodic refresh (default 5s, opt-in via
//     [WithRefreshInterval]).
//   - Store is pluggable: [MemoryStore] for single-pod dev, a Redis
//     store for multi-pod fleets (see runbook/runbookredis).
//
// Audit: every Set emits an [audit.Event] when [WithAuditor] is
// wired — so the compliance trail captures who flipped what when.
// Without the auditor the flag still flips, just unaudited.
package runbook
