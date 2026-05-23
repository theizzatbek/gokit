package tasks

import (
	"errors"
	"strings"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/theizzatbek/fibermap/examples/tasks/internal/appctx"
)

// Handler bundles store + validator + handler funcs so they're easy
// to wire up in main and to swap out for tests.
//
// Validator is injected (constructor argument) rather than package-
// global so tests can stub it. It's reused across requests — a
// *validator.Validate is goroutine-safe.
type Handler struct {
	Store     Store
	Validator *validator.Validate
}

// New returns a Handler over the given Store + validator.
func New(s Store, v *validator.Validate) *Handler {
	return &Handler{Store: s, Validator: v}
}

// List handles GET /tasks — returns the caller's tasks.
func (h *Handler) List(c *appctx.Ctx) error {
	out := h.Store.ListByOwner(c.Data.UserID)
	if out == nil {
		out = []Task{} // never serialize null — clients hate it
	}
	return c.JSON(ListResponse{Tasks: out})
}

// Get handles GET /tasks/:id.
func (h *Handler) Get(c *appctx.Ctx) error {
	t, err := h.Store.Get(c.Data.UserID, c.Params("id"))
	if errors.Is(err, ErrNotFound) {
		return notFound(c)
	}
	return c.JSON(t)
}

// CreateReq is the POST /tasks body. validate: tags do the work via
// go-playground/validator; bind.Body[T] is the one-liner that parses
// and validates. Exported so external code (e.g. main.go's OpenAPI
// generator wiring) can reference the type.
type CreateReq struct {
	Title string `json:"title" validate:"required,min=1,max=200"`
}

// Create handles POST /tasks. Wired with fibermap.RegisterHandlerWithBody, so
// `req` arrives already parsed + validated; bind.Body call and
// per-handler error branching live in fibermap, not here.
func (h *Handler) Create(c *appctx.Ctx, req CreateReq) error {
	req.Title = strings.TrimSpace(req.Title)
	t := h.Store.Create(c.Data.UserID, req.Title)
	c.Data.Log.Info("task created", "task_id", t.ID, "title", t.Title)
	return c.Status(fiber.StatusCreated).JSON(t)
}

// UpdateReq is the PATCH /tasks/:id body. Pointer fields let us
// distinguish "not provided" from "set to zero value" — important
// because PATCH semantics are "update only the fields present".
// `omitempty` on validate skips the rule when the pointer is nil.
type UpdateReq struct {
	Title *string `json:"title,omitempty" validate:"omitempty,min=1,max=200"`
	Done  *bool   `json:"done,omitempty"`
}

// Update handles PATCH /tasks/:id. Same wiring as Create — fibermap
// fills `req` before calling.
func (h *Handler) Update(c *appctx.Ctx, req UpdateReq) error {
	if req.Title == nil && req.Done == nil {
		// Cross-field rule that doesn't fit a struct tag — keep the
		// hand-rolled check here. fibermap.RegisterHandlerWithBody covers
		// per-field validate; "at least one of" stays in code.
		return badRequest(c, "at least one of title, done must be present")
	}
	if req.Title != nil {
		trimmed := strings.TrimSpace(*req.Title)
		req.Title = &trimmed
	}

	t, err := h.Store.Update(c.Data.UserID, c.Params("id"), req.Title, req.Done)
	if errors.Is(err, ErrNotFound) {
		return notFound(c)
	}
	c.Data.Log.Info("task updated", "task_id", t.ID)
	return c.JSON(t)
}

// Delete handles DELETE /tasks/:id. Mounted with require_role: [admin]
// in routes.yaml — only admins reach this handler at all. The handler
// itself then uses the unscoped AdminDelete so admins can delete ANY
// user's task, not just their own. This is the "route auth ≠ data
// auth" pattern: YAML's require_role lets you in the door; the
// handler picks the right data-access method based on intent.
func (h *Handler) Delete(c *appctx.Ctx) error {
	id := c.Params("id")
	if err := h.Store.AdminDelete(id); err != nil {
		if errors.Is(err, ErrNotFound) {
			return notFound(c)
		}
		return c.Status(fiber.StatusInternalServerError).JSON(ErrorResponse{Error: err.Error()})
	}
	c.Data.Log.Info("task deleted by admin", "task_id", id, "admin", c.Data.UserID)
	return c.SendStatus(fiber.StatusNoContent)
}

func notFound(c *appctx.Ctx) error {
	return c.Status(fiber.StatusNotFound).JSON(ErrorResponse{Error: "task not found"})
}

func badRequest(c *appctx.Ctx, msg string) error {
	return c.Status(fiber.StatusBadRequest).JSON(ErrorResponse{Error: msg})
}
