// Package auth holds the Fiber-level auth middleware (runs BEFORE
// fibermap's ContextBuilder so it can stash user info in Locals)
// and the fibermap-level require_role factory.
package auth

import (
	"encoding/base64"
	"errors"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/theizzatbek/gokit/examples/tasks/internal/appctx"
	"github.com/theizzatbek/gokit/fibermap"
	"golang.org/x/crypto/bcrypt"
)

// demoUser is the resolved identity an auth middleware writes to
// Locals — same shape regardless of which scheme produced it.
type demoUser struct {
	UserID       string
	Role         string
	passwordHash string // unexported — never serialized
}

// Demo token table — in a real app, this is a JWT verifier hitting
// the public key, an opaque-token lookup against Redis, etc. The
// shape (token -> {user_id, role}) stays the same.
var demoTokens = map[string]demoUser{
	"alice-token": {UserID: "u-alice", Role: "user"},
	"bob-token":   {UserID: "u-bob", Role: "user"},
	"root-token":  {UserID: "u-root", Role: "admin"},
}

// Demo Basic-auth credentials. Passwords stored as bcrypt hashes
// (cost 10) — plaintext is for the README, not the source.
//
// Regenerate via:
//
//	hash, _ := bcrypt.GenerateFromPassword([]byte("new-password"), bcrypt.DefaultCost)
//	fmt.Println(string(hash))
//
// Default logins (for the README + quick curl):
//
//	alice : secret
//	bob   : secret
//	root  : admin
var demoBasic = map[string]demoUser{
	"alice": {UserID: "u-alice", Role: "user", passwordHash: "$2a$10$bzi.7kkcBVEhP1Im/VKcmePqrGr/aPEuzRIOqQdlE.EnrYvSjckJ6"},
	"bob":   {UserID: "u-bob", Role: "user", passwordHash: "$2a$10$DvaFoDRCWCltEHkIp.zTtOuUzRRFS8jIbfk9B392DH8eGU.bePLja"},
	"root":  {UserID: "u-root", Role: "admin", passwordHash: "$2a$10$qz8SXSRLRyD72noufvqxsee1KJtgUn6oL4QZc9vY5wZ8tQdMIU3E."},
}

func writeUser(c *fiber.Ctx, u demoUser) error {
	c.Locals("user_id", u.UserID)
	c.Locals("role", u.Role)
	return c.Next()
}

// Bearer parses `Authorization: Bearer <token>`, looks the token up
// in the demo table, and stashes user_id + role on c.Locals. 401 on
// missing or unknown token.
func Bearer() fiber.Handler {
	return func(c *fiber.Ctx) error {
		const prefix = "Bearer "
		h := c.Get("Authorization")
		if !strings.HasPrefix(h, prefix) {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "missing bearer token"})
		}
		u, ok := demoTokens[strings.TrimPrefix(h, prefix)]
		if !ok {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "invalid token"})
		}
		return writeUser(c, u)
	}
}

// Basic parses `Authorization: Basic <base64(user:pass)>`, looks up
// the user in the demo table, and verifies the password against the
// stored bcrypt hash. On success, stashes user_id + role on c.Locals.
//
// Every failure branch — missing header, bad base64, unknown user,
// wrong password — returns an identical 401 body. Distinct messages
// would leak whether a username exists (user-enumeration).
func Basic() fiber.Handler {
	// invalidCreds is the one and only 401 body Basic emits to clients.
	const invalidCreds = `{"error":"invalid basic credentials"}`
	unauthorized := func(c *fiber.Ctx) error {
		c.Status(fiber.StatusUnauthorized)
		c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
		return c.SendString(invalidCreds)
	}
	return func(c *fiber.Ctx) error {
		const prefix = "Basic "
		h := c.Get("Authorization")
		if !strings.HasPrefix(h, prefix) {
			return unauthorized(c)
		}
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(h, prefix))
		if err != nil {
			return unauthorized(c)
		}
		user, pass, ok := strings.Cut(string(decoded), ":")
		if !ok {
			return unauthorized(c)
		}
		u, ok := demoBasic[user]
		if !ok {
			return unauthorized(c)
		}
		if err := bcrypt.CompareHashAndPassword([]byte(u.passwordHash), []byte(pass)); err != nil {
			return unauthorized(c)
		}
		return writeUser(c, u)
	}
}

// BearerOrBasic accepts either a Bearer token or a Basic
// (user:password) Authorization header, dispatching to the
// corresponding scheme. Used in the demo so /docs can show both
// auth options in the OpenAPI spec.
//
// `publicPaths` opt certain paths out of authentication entirely
// (the middleware calls c.Next without consulting any scheme). Pass
// the OpenAPI spec/docs paths here so they're browsable from a
// vanilla browser without credentials. Typical:
//
//	auth.BearerOrBasic("/docs", "/openapi.json")
//
// Order: skip-list first, then inspect the Authorization header
// prefix. No prefix → 401 without consulting either scheme.
func BearerOrBasic(publicPaths ...string) fiber.Handler {
	skip := make(map[string]struct{}, len(publicPaths))
	for _, p := range publicPaths {
		skip[p] = struct{}{}
	}
	bearer := Bearer()
	basic := Basic()
	return func(c *fiber.Ctx) error {
		if _, public := skip[c.Path()]; public {
			return c.Next()
		}
		h := c.Get("Authorization")
		switch {
		case strings.HasPrefix(h, "Bearer "):
			return bearer(c)
		case strings.HasPrefix(h, "Basic "):
			return basic(c)
		default:
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "missing credentials (Bearer or Basic)"})
		}
	}
}

// RequireRole returns a MiddlewareFactoryFunc to register with the
// engine via:
//
//	eng.RegisterMiddlewareFactory("require_role", auth.RequireRole)
//
// YAML calls it as `{require_role: [admin]}` or `{require_role: [user, admin]}`.
func RequireRole(args []string) (appctx.MW, error) {
	if len(args) == 0 {
		return nil, errors.New("require_role: at least one role required")
	}
	allowed := append([]string(nil), args...)
	return func(c *appctx.Ctx) error {
		for _, r := range allowed {
			if r == c.Data.Role {
				return c.Next()
			}
		}
		// Use the request-scoped logger so the auth denial is correlated
		// with the rest of the request log line.
		c.Data.Log.Warn("authz denied",
			"required_roles", allowed,
			"actual_role", c.Data.Role,
			"path", c.Path())
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error":          "forbidden",
			"required_roles": allowed,
			"current_role":   c.Data.Role,
		})
	}, nil
}

// Confirm the factory satisfies fibermap's signature at compile time.
var _ fibermap.MiddlewareFactoryFunc[appctx.AppCtx] = RequireRole
