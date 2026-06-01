// Package auditmw is a Fiber middleware that auto-emits audit
// events from each inbound HTTP request. It closes the wiring gap
// for the audit package: instead of every handler manually calling
// audit.Log(), one middleware emits per-request entries with
// sensible defaults pulled from the routing layer (method + route
// pattern) and the auth context (subject + IP).
//
//	app.Use(auditmw.Middleware(svc.Audit,
//	    auditmw.WithSubject(func(c *fiber.Ctx) string {
//	        if p, ok := auth.From[MyClaims](c); ok {
//	            return p.Subject
//	        }
//	        return ""
//	    }),
//	    auditmw.WithSkipPaths("/healthz", "/readyz", "/metrics", "/preflight"),
//	))
//
// Default policy:
//
//   - Only mutating methods are logged (POST / PUT / PATCH / DELETE).
//     Read-traffic is too noisy for an audit log; flip the policy
//     via [WithIncludeMethods] if your compliance regime requires
//     reads to be recorded.
//   - Action verb is `<method>.<route-pattern>` (e.g.
//     `POST./api/tasks`). Override per-route with [WithActionFn] or
//     mark a route to inherit a static action via [WithStaticAction].
//   - Outcome is derived from status code:
//     2xx → success, 4xx authorization codes → denied, other 4xx/5xx
//     → failure.
//   - Subject is extracted via [WithSubject]; without it the
//     middleware logs the actor IP only (Actor.Subject stays empty).
//
// The middleware is fail-soft — audit.Log errors are logged at
// Warn but never propagate as a 500 to the client. An audit blip
// must NEVER turn a successful write into a failed response.
package auditmw
