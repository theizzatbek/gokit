package tasks

import (
	"errors"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/theizzatbek/fibermap/examples/tasks/internal/appctx"
)

// Handler bundles store + handler funcs so they're easy to wire up in
// main and to swap the store in tests.
type Handler struct {
	Store Store
}

// New returns a Handler over the given Store.
func New(s Store) *Handler { return &Handler{Store: s} }

// List handles GET /tasks — returns the caller's tasks.
func (h *Handler) List(c *appctx.Ctx) error {
	out := h.Store.ListByOwner(c.Data.UserID)
	if out == nil {
		out = []Task{} // never serialize null — clients hate it
	}
	return c.JSON(fiber.Map{"tasks": out})
}

// Get handles GET /tasks/:id.
func (h *Handler) Get(c *appctx.Ctx) error {
	t, err := h.Store.Get(c.Data.UserID, c.Params("id"))
	if errors.Is(err, ErrNotFound) {
		return notFound(c)
	}
	return c.JSON(t)
}

// createReq is the POST /tasks body. JSON tags only — validation
// happens in the handler since fibermap doesn't ship a validator.
type createReq struct {
	Title string `json:"title"`
}

// Create handles POST /tasks.
func (h *Handler) Create(c *appctx.Ctx) error {
	var req createReq
	if err := c.BodyParser(&req); err != nil {
		return badRequest(c, "invalid JSON body")
	}
	req.Title = strings.TrimSpace(req.Title)
	if req.Title == "" {
		return badRequest(c, "title is required")
	}
	if len(req.Title) > 200 {
		return badRequest(c, "title must be 200 characters or fewer")
	}

	t := h.Store.Create(c.Data.UserID, req.Title)
	c.Data.Log.Info("task created", "task_id", t.ID, "title", t.Title)
	return c.Status(fiber.StatusCreated).JSON(t)
}

// updateReq is the PATCH /tasks/:id body. Pointer fields let us
// distinguish "not provided" from "set to zero value" — important
// because PATCH semantics are "update only the fields present".
type updateReq struct {
	Title *string `json:"title,omitempty"`
	Done  *bool   `json:"done,omitempty"`
}

// Update handles PATCH /tasks/:id.
func (h *Handler) Update(c *appctx.Ctx) error {
	var req updateReq
	if err := c.BodyParser(&req); err != nil {
		return badRequest(c, "invalid JSON body")
	}
	if req.Title == nil && req.Done == nil {
		return badRequest(c, "at least one of title, done must be present")
	}
	if req.Title != nil {
		title := strings.TrimSpace(*req.Title)
		if title == "" {
			return badRequest(c, "title cannot be empty")
		}
		if len(title) > 200 {
			return badRequest(c, "title must be 200 characters or fewer")
		}
		req.Title = &title
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
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	c.Data.Log.Info("task deleted by admin", "task_id", id, "admin", c.Data.UserID)
	return c.SendStatus(fiber.StatusNoContent)
}

func notFound(c *appctx.Ctx) error {
	return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "task not found"})
}

func badRequest(c *appctx.Ctx, msg string) error {
	return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": msg})
}
