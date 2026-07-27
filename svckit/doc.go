// Package svckit — the modular service core.
//
// The core builds DB, Auth, an HTTP client and the fibermap engine.
// It knows nothing about optional subsystems (S3, Redis, NATS, OTel,
// …): those live in mods under svckit/mods/ and only end up in the
// binary when the application referenced the mod from main. That is
// the whole point of the package — the linker drops what is
// unreachable.
//
// Lifecycle:
//
//	Setup(mods) → logger → DB → migrate → Auth → HTTPC
//	    → Build(mods) → Engine → auth factories → Wire(mods) → cron
//
// A mod implements Mod plus whichever of Setuper / Builder / Wirer it
// needs. Resource cleanup goes through Host.OnShutdown, LIFO relative
// to construction order.
//
// The service package (v1) stays functional and unchanged: it doesn't
// know about mods at all — it calls every optional subsystem's
// constructor directly (buildS3, buildRedis, buildNATS, ...), so it
// keeps the binary size it always had regardless of what an
// individual service actually configures.
package svckit
