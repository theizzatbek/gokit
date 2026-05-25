// Package auth provides a complete authentication bundle for fibermap services:
// asymmetrically-signed JWT access tokens (EdDSA / ES256), opaque rotation-aware
// refresh tokens with reuse detection, argon2id password hashing, and ready-to-mount
// Login/Refresh/Logout handlers + Bearer/RequireScope/RequireRole middleware.
//
// The core auth package depends on stdlib + crypto + golang-jwt + fiber and never
// imports db or redis. Two opt-in subpackages provide refresh stores:
//
//	auth/refreshpg     - Postgres-backed RefreshStore over *db.DB
//	auth/refreshredis  - Redis-backed RefreshStore over go-redis/v9
//
// Typical wiring:
//
//	import "github.com/theizzatbek/fibermap/auth/fibermount"
//
//	keys, _ := auth.GenerateEd25519Key("k1")
//	store   := refreshpg.New(database)
//	a, _    := auth.New[MyClaims](auth.Config{
//	    Issuer: "myapp", Audience: []string{"web"},
//	    Keys: keys, AccessTTL: 15*time.Minute, RefreshTTL: 30*24*time.Hour,
//	}, auth.WithRefreshStore(store))
//	a.SetCredentialsVerifier(myVerifier)
//	fibermount.MountMiddlewareFactories(eng, a)
//	eng.RegisterHandler("auth.login",   a.LoginHandler)
//	eng.RegisterHandler("auth.refresh", a.RefreshHandler)
//	eng.RegisterHandler("auth.logout",  a.LogoutHandler)
//
// See docs/superpowers/specs/2026-05-24-kit-auth-design.md for the full design.
package auth
