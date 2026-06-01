// Package auditadmin is a small browser UI for the audit log:
// search by actor / action / outcome / time-range, paginate, export
// the filtered set as JSON for compliance review.
//
// Mount under an auth-gated path — the UI exposes everything in the
// audit table including PII inside Metadata. Treat it as sensitive
// like /metrics; never put it on a public LB.
//
//	app.Use("/admin", auth.Bearer(auth.BearerRequired),
//	    auth.RequireRole("compliance"))
//	auditadmin.Mount(app, "/admin/audit", svc.Audit)
//
// Output is plain HTML + inline CSS — no external assets, no JS
// framework, no live updates. The UI is purpose-built for ad-hoc
// triage by a human; for programmatic exports use logger.Query()
// directly or the /admin/audit.json endpoint this package mounts
// alongside the HTML view.
package auditadmin
