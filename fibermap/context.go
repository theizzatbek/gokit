package fibermap

import "github.com/gofiber/fiber/v2"

// Context is the per-request value passed to fibermap handlers and middleware.
// It embeds *fiber.Ctx so every Fiber method is available directly, and adds
// Data of type T — populated once per request by the ContextBuilder set on
// the engine.
type Context[T any] struct {
	*fiber.Ctx
	Data T
}
