package links

import (
	xerrs "github.com/theizzatbek/gokit/errs"
	"github.com/theizzatbek/gokit/examples/urlshort/urlshort-api/internal/appctx"
	"github.com/theizzatbek/gokit/fibermap"
)

// Handler bundles the deps every links endpoint needs. Methods read
// cleanly one per endpoint; RegisterHandlers is a thin registrar that
// wires fibermap names to methods.
type Handler struct {
	svc          *Service
	shortURLBase string
}

// NewHandler constructs a Handler. shortURLBase is the public origin
// used to render short_url in CreateResponse — main.go passes
// cfg.ShortURLBase.
func NewHandler(svc *Service, shortURLBase string) *Handler {
	return &Handler{svc: svc, shortURLBase: shortURLBase}
}

// RegisterHandlers wires every link endpoint by name onto eng.
//
// Handlers that read :code use RegisterHandlerWithParams so the value
// is parsed + validated (len=6, alphanum) by the bind layer before the
// handler runs — invalid codes return 400 without touching the service.
//
// links.update showcases fibermap.RegisterHandlerWithInput: PATCH
// /links/:code combines the path param (CodeParams) with a JSON body
// (UpdateRequest), and a single UpdateInput struct binds + validates
// both at once.
func RegisterHandlers(eng *fibermap.Engine[appctx.AppCtx], svc *Service, shortURLBase string) {
	h := NewHandler(svc, shortURLBase)
	fibermap.RegisterHandlerWithBody(eng, "links.create", h.Create)
	fibermap.RegisterHandlerWithQuery(eng, "links.list", h.List)
	fibermap.RegisterHandlerWithParams(eng, "links.redirect", h.Redirect)
	fibermap.RegisterHandlerWithParams(eng, "links.stats", h.Stats)
	fibermap.RegisterHandlerWithParams(eng, "links.delete", h.Delete)
	fibermap.RegisterHandlerWithInput(eng, "links.update", h.Update)
}

// Create handles POST /links — shortens a URL for the current user.
func (h *Handler) Create(c *fibermap.Context[appctx.AppCtx], body CreateRequest) error {
	l, err := h.svc.Create(c.UserContext(), c.Data.UserID, body.URL)
	if err != nil {
		return err
	}
	// Request-scoped logger automatically carries method, path,
	// request_id, user_id, and route — handler only adds the new
	// attrs it cares about.
	fibermap.LoggerFrom(c.Ctx).Info("link created",
		"code", l.Code, "original_url", l.OriginalURL)
	return c.Status(201).JSON(CreateResponse{
		Code:        l.Code,
		ShortURL:    h.shortURLBase + "/" + l.Code,
		Title:       l.Title,
		Description: l.Description,
		ImageURL:    l.ImageURL,
	})
}

// List handles GET /links?limit=&offset=&q= — returns the user's links
// paginated, optionally filtered by case-insensitive substring search
// on title and original_url.
//
// ListParams is the local composition of the kit's sqb.Page (limit,
// offset) and an endpoint-specific Q field — Go struct embedding lets
// the kit primitive stay tiny while the service layers on its own
// filters per endpoint.
func (h *Handler) List(c *fibermap.Context[appctx.AppCtx], p ListParams) error {
	ls, err := h.svc.ListByUser(c.UserContext(), c.Data.UserID, p)
	if err != nil {
		return err
	}
	return c.JSON(ls)
}

// Redirect handles GET /:code — public 302 to the original URL,
// recording a visit asynchronously.
func (h *Handler) Redirect(c *fibermap.Context[appctx.AppCtx], p CodeParams) error {
	l, err := h.svc.Resolve(c.UserContext(), p.Code, c.Get("User-Agent"), c.IP())
	if err != nil {
		return err
	}
	return c.Redirect(l.OriginalURL, 302)
}

// Stats handles GET /links/:code/stats — owner-only visit metrics.
func (h *Handler) Stats(c *fibermap.Context[appctx.AppCtx], p CodeParams) error {
	l, err := h.svc.GetByCode(c.UserContext(), p.Code)
	if err != nil {
		return err
	}
	if l.UserID != c.Data.UserID {
		return xerrs.Permission("link_not_owned", "urlshort: link belongs to a different user")
	}
	return c.JSON(StatsResponse{
		Code:          l.Code,
		VisitCount:    l.VisitCount,
		LastVisitedAt: l.LastVisitedAt,
		CreatedAt:     l.CreatedAt,
	})
}

// Delete handles DELETE /links/:code — owner-only removal.
func (h *Handler) Delete(c *fibermap.Context[appctx.AppCtx], p CodeParams) error {
	if err := h.svc.Delete(c.UserContext(), p.Code, c.Data.UserID); err != nil {
		return err
	}
	return c.SendStatus(204)
}

// Update handles PATCH /links/:code — owner-only partial update of
// title and/or description. Both the path :code and the JSON body
// arrive parsed + validated via fibermap.RegisterHandlerWithInput; the
// handler itself only mediates the owner check via the service.
func (h *Handler) Update(c *fibermap.Context[appctx.AppCtx], in UpdateInput) error {
	l, err := h.svc.Update(c.UserContext(), in.Params.Code, c.Data.UserID, in.Body)
	if err != nil {
		return err
	}
	return c.JSON(l)
}
