package fibermap

import (
	"crypto/rand"
	"encoding/hex"

	"github.com/gofiber/fiber/v2"
)

// LocalsRequestID is the Fiber Locals key under which [RequestID]
// stores the per-request identifier. Read it from a ContextBuilder
// via `c.Locals(fibermap.LocalsRequestID).(string)`.
const LocalsRequestID = "request_id"

// HeaderRequestID is the HTTP header [RequestID] reads from and
// writes to. Matches the de-facto convention for request correlation.
const HeaderRequestID = "X-Request-ID"

// RequestID returns a Fiber-level middleware that ensures every
// request carries an `X-Request-ID`:
//
//  1. If the incoming request has an `X-Request-ID` header, use it as-is.
//  2. Otherwise generate a fresh 16-hex-character identifier from
//     crypto/rand.
//  3. Stash the value on `c.Locals(fibermap.LocalsRequestID)` so the
//     engine's ContextBuilder can read it without re-parsing the
//     header.
//  4. Echo the value back as the response `X-Request-ID` header so
//     callers can correlate logs.
//
// Install via [Engine.Run] with [WithUse], BEFORE any auth
// middleware — the ID should appear in auth-failure logs too:
//
//	eng.Run(fibermap.WithUse(fibermap.RequestID(), auth.Bearer()))
//
// Or via plain fiber:
//
//	app.Use(fibermap.RequestID())
//
// For different header/key conventions, copy the eight-line body
// of this function and adjust.
func RequestID() fiber.Handler {
	return func(c *fiber.Ctx) error {
		id := c.Get(HeaderRequestID)
		if id == "" {
			var b [8]byte
			_, _ = rand.Read(b[:])
			id = hex.EncodeToString(b[:])
		}
		c.Locals(LocalsRequestID, id)
		c.Set(HeaderRequestID, id)
		return c.Next()
	}
}
