// Package webhooks implements outbound + inbound HTTP webhook
// infrastructure for the kit: typed Subscription / Delivery rows
// persisted by an implementation of SubscriptionStore + DeliveryStore,
// a Fanout that turns one domain event into N per-target deliveries,
// a Worker that drains pending deliveries with per-target retry and
// dead-letter, an outbound Signer producing Stripe-style HMAC
// signatures, and a Verifier interface (with two ready-made
// implementations in clients/webhooks/verifiers) for inbound payloads.
//
// Storage is interface-only here; the canonical Postgres implementation
// lives in clients/webhooks/storepg.
//
// The package depends on errs/, clients/httpc/, and (optionally,
// inside Worker.Start) prometheus/client_golang. It does NOT import
// db/, auth/, fiber, or nats — those couplings live in the storepg
// subpackage and clients/webhooks's own consumers (service/, fibermap/
// webhookguard) respectively.
package webhooks
