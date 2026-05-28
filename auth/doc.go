// Package auth provides a complete authentication bundle for fibermap services:
// asymmetrically-signed JWT access tokens (EdDSA / ES256), opaque rotation-aware
// refresh tokens with reuse detection, argon2id password hashing, the
// IssueTokens / IssueLogin / IssueRefresh / Logout primitives, and
// Bearer/RequireScope/RequireRole middleware.
//
// The core auth package depends on stdlib + crypto + golang-jwt + fiber and never
// imports db or redis. Two opt-in subpackages provide refresh stores:
//
//	auth/refreshpg     - Postgres-backed RefreshStore over *db.DB
//	auth/refreshredis  - Redis-backed RefreshStore over go-redis/v9
//
// The kit deliberately does NOT own the wire body of /auth/login — every
// service declares its own request type (password, mTLS, PKCS7-signed
// payload, OIDC id_token, …), verifies the credential, and hands the
// resulting LoginResult[C] to IssueLogin. Token minting + refresh-cookie
// management stay in the kit.
//
// Typical wiring:
//
//	import "github.com/theizzatbek/gokit/auth/fibermount"
//
//	keys, _ := auth.GenerateEd25519Key("k1")
//	store   := refreshpg.New(database)
//	a, _    := auth.New[MyClaims](auth.Config{
//	    Issuer: "myapp", Audience: []string{"web"},
//	    Keys: keys, AccessTTL: 15*time.Minute, RefreshTTL: 30*24*time.Hour,
//	}, auth.WithRefreshStore(store))
//
//	// Bearer / RequireScope / RequireRole middleware factories.
//	fibermount.MountMiddlewareFactories(eng, a)
//
//	// Your custom login handler — body shape and credential check are yours.
//	app.Post("/auth/login", func(c *fiber.Ctx) error {
//	    var body LoginRequest
//	    if err := c.BodyParser(&body); err != nil { return ... }
//	    user, err := usersSvc.Authenticate(c.UserContext(), body.Login, body.Password)
//	    if err != nil { return err }
//	    return a.IssueLogin(c, auth.LoginResult[MyClaims]{
//	        Subject: user.ID,
//	        Custom:  MyClaims{Email: user.Email},
//	    })
//	})
//
//	// Refresh + logout are one-liners — no service-side body parsing.
//	app.Post("/auth/refresh", a.IssueRefresh)
//	app.Post("/auth/logout",  a.Logout)
//
// See docs/superpowers/specs/2026-05-24-kit-auth-design.md for the full design.
package auth
