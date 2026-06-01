// Package natsgw is a generic HTTP→NATS gateway middleware. Mount
// it on a Fiber router and inbound requests get republished onto
// the matching NATS subject via natsmap.PublishRaw.
//
// Use cases:
//
//   - Edge gateway — HTTP-only services publish to NATS via this
//     gateway, keeping the natsmap import surface out of their own
//     binaries (the urlshort-api → urlshort-publisher pattern in
//     examples/).
//   - Polyglot fleets — services written in languages without a
//     mature NATS client can POST JSON instead.
//   - Webhook ingestion — external systems POST events that should
//     flow into the internal NATS bus.
//
// Default wiring:
//
//	app.Post("/publish/:subject", natsgw.Handler(svc.NATSMap,
//	    natsgw.WithSubjectAllowlist(
//	        "urlshort.link.created",
//	        "urlshort.link.visited",
//	    ),
//	    natsgw.WithHeaderForwarder("X-Tenant"),
//	))
//
// The subject is pulled from the path param `:subject`; the request
// body is the raw payload forwarded verbatim to natsmap.PublishRaw.
// Override the subject extractor via [WithSubjectExtractor].
//
// Security:
//
//   - Without [WithSubjectAllowlist], ANY subject registered in
//     publishers.yaml is publishable through this gateway. Most
//     deployments should set an allowlist explicitly — wide open
//     publishing is rarely intended.
//   - The gateway does NOT authenticate by itself. Wire your auth
//     middleware in front of the Mount call (Bearer + RequireRole,
//     api-key, mutual-TLS at the LB layer, etc.).
//   - Header forwarding is opt-in via [WithHeaderForwarder] —
//     callers' raw headers do NOT flow into NATS messages by
//     default. The kit's X-Request-ID auto-propagates from ctx
//     regardless.
package natsgw
