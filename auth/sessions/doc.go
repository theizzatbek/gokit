// Package sessions adds server-side cookie sessions to the JWT-first
// auth package. Use it when browser-rendered apps need:
//
//   - Server-side revocation (admin clicks "log out everywhere"
//     and every active session ends within one round-trip — JWTs
//     can't do this without a blocklist).
//   - Sliding inactivity timeout — sessions extend on every hit
//     and expire after IdleTimeout of silence.
//   - First-party cookie auth — no Authorization-header plumbing
//     in HTML forms / fetch + credentials: 'include'.
//
// Sessions coexist with JWT: services can mount both Bearer + Session
// middleware on the engine; the first one to populate the Locals
// principal wins. Login flows pick which to issue — APIs return a
// JWT, web flows set a session cookie.
//
// SessionStore is the persistence interface. The kit ships
// auth/sessionsredis as the default backend. Roll a custom store
// (postgres / dynamo / in-memory for tests) by implementing it.
//
//	// Wiring:
//	sm := auth.Sessions(svc.Sessions, sessions.Config{
//	    CookieName:  "sid",
//	    TTL:         24 * time.Hour,
//	    IdleTimeout: time.Hour,
//	    Secure:      true,
//	})
//
//	// Login route — issue a session.
//	app.Post("/login", func(c *fiber.Ctx) error {
//	    // … verify credentials …
//	    return sm.Issue(c, "u-42", MyClaims{Plan: "pro"}, nil, nil)
//	})
//
//	// Protect routes — same Principal[C] surface as auth.Bearer.
//	app.Use(sm.Middleware(sessions.Required))
//
// Cookie defaults are secure-first: HttpOnly, Secure, SameSite=Lax,
// Path=/. Override per [Config] field.
package sessions
