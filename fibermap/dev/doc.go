// Package dev wires the kit's dev-only DX tooling — HTML error pages
// with stack traces, route/config inspectors. Enable via
// [service.WithDevMode] in the service constructor, or mount the
// individual handlers manually in non-service Fiber apps.
//
// The package is intentionally lightweight: no JavaScript, no
// external CSS, no asset embedding. Output is plain HTML that
// renders in any browser including curl-rendered terminals via
// `--accept text/html`.
//
//	svc, _ := service.New[App, Claims](ctx, cfg,
//	    service.WithDevMode("/_dev"),  // mounts /_dev/routes + /_dev/config
//	)
//
// Auto-disabled when ENV != "dev" (the [service.WithDevMode] helper
// checks Config.Service.Env). For non-service Fiber apps mount via
// dev.RoutesHandler + dev.ConfigHandler directly.
//
// Security: the inspectors expose the route table and the effective
// config (with secrets redacted). Treat them as sensitive — never
// route them through a public load balancer in production. The
// service-level helper refuses to mount when ENV != "dev" as a
// belt-and-braces guard.
package dev
