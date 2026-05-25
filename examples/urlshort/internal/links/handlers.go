package links

import (
	xerrs "github.com/theizzatbek/gokit/errs"
	"github.com/theizzatbek/gokit/examples/urlshort/internal/appctx"
	"github.com/theizzatbek/gokit/fibermap"
	"github.com/theizzatbek/gokit/fibermap/bind"
)

// RegisterHandlers wires every link endpoint. shortURLBase is the
// public origin used to render short_url in CreateResponse — main.go
// passes cfg.ShortURLBase.
func RegisterHandlers(eng *fibermap.Engine[appctx.AppCtx], svc *Service, shortURLBase string) {
	fibermap.RegisterHandler(eng, "links.create",
		func(c *fibermap.Context[appctx.AppCtx]) error {
			body, err := bind.Body[CreateRequest](c.Ctx, nil)
			if err != nil {
				return err
			}
			l, err := svc.Create(c.UserContext(), c.Data.UserID, body.URL)
			if err != nil {
				return err
			}
			return c.Status(201).JSON(CreateResponse{
				Code:        l.Code,
				ShortURL:    shortURLBase + "/" + l.Code,
				Title:       l.Title,
				Description: l.Description,
				ImageURL:    l.ImageURL,
			})
		})

	fibermap.RegisterHandler(eng, "links.list",
		func(c *fibermap.Context[appctx.AppCtx]) error {
			ls, err := svc.ListByUser(c.UserContext(), c.Data.UserID)
			if err != nil {
				return err
			}
			return c.JSON(ls)
		})

	fibermap.RegisterHandler(eng, "links.redirect",
		func(c *fibermap.Context[appctx.AppCtx]) error {
			l, err := svc.IncVisit(c.UserContext(), c.Params("code"), c.Get("User-Agent"), c.IP())
			if err != nil {
				return err
			}
			return c.Redirect(l.OriginalURL, 302)
		})

	fibermap.RegisterHandler(eng, "links.stats",
		func(c *fibermap.Context[appctx.AppCtx]) error {
			l, err := svc.GetByCode(c.UserContext(), c.Params("code"))
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
		})

	fibermap.RegisterHandler(eng, "links.delete",
		func(c *fibermap.Context[appctx.AppCtx]) error {
			if err := svc.Delete(c.UserContext(), c.Params("code"), c.Data.UserID); err != nil {
				return err
			}
			return c.SendStatus(204)
		})
}
