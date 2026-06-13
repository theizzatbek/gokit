// Package auditfm wires the [audit] package into [fibermap] handler
// registration: a single per-handler decorator emits the audit event
// AFTER the handler returns, derived from the spec (Action / Target /
// Subject / Metadata) the caller declared at registration time.
//
// Audit emission is declarative at the registration site:
//
//	fibermap.RegisterHandler(eng, "license.revoke",
//	    auditfm.Wrap[AppCtx](audit, auditfm.Spec{
//	        Action: "license.revoke",
//	        TargetFn: func(c *fiber.Ctx) audit.Target {
//	            return audit.Target{Type: "license", ID: c.Params("id")}
//	        },
//	    }, h.RevokeLicense),
//	)
//
// For typed-bind handlers (RegisterHandlerWithBody / WithParams /
// WithQuery / WithHeaders / WithInput), the wrap-around shape works
// through a deferred [Emit] inside the handler body. See the README
// for the recipe.
//
// vs auditmw
//
// [audit/auditmw] (app-level Fiber middleware) emits one event per
// matching request based on HTTP method / path / a caller-supplied
// classifier. It's the "audit every privileged endpoint with one
// rule" model. auditfm is the "audit this specific endpoint with
// this specific spec" model — declared at the handler-registration
// call site, next to the handler itself.
//
// Use auditmw for blanket policies ("every POST under /admin is
// audited as Action=$path") and auditfm for per-handler precision
// (each privileged route names its own Action / Target shape).
// Mixing the two is fine — auditfm runs inside the handler scope,
// auditmw wraps the whole request.
//
// # Outcome classification
//
// The default classifier maps the handler's returned error to an
// [audit.Outcome]:
//
//	nil                                                 → Success
//	*errs.Error{Kind: Unauthorized | Permission}        → Denied
//	context.Canceled / context.DeadlineExceeded         → Failure
//	anything else                                       → Failure
//
// Override via [Spec.OutcomeFn] when you need a custom mapping
// (e.g. a typed validation error should be Denied rather than
// Failure for a particular endpoint).
package auditfm
