// Command urlshort is the gokit integration example — a URL-shortener
// service that uses every kit package in its natural role, wired
// through gokit/service.New.
//
// Run:
//
//	make up && make run    # local Postgres + NATS + service
//	curl -X POST http://localhost:3000/auth/register -H 'content-type: application/json' \
//	  -d '{"email":"a@b.com","password":"hunter2hunter2"}'
//	# … login → shorten → redirect → stats. See README.md for the full walkthrough.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/theizzatbek/gokit/clients/apimap"
	natsclient "github.com/theizzatbek/gokit/clients/nats"
	"github.com/theizzatbek/gokit/clients/natsmap"
	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/examples/urlshort/internal/appctx"
	"github.com/theizzatbek/gokit/examples/urlshort/internal/config"
	"github.com/theizzatbek/gokit/examples/urlshort/internal/enrich"
	"github.com/theizzatbek/gokit/examples/urlshort/internal/events"
	"github.com/theizzatbek/gokit/examples/urlshort/internal/links"
	"github.com/theizzatbek/gokit/examples/urlshort/internal/users"
	"github.com/theizzatbek/gokit/fibermap/openapi"
	"github.com/theizzatbek/gokit/service"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	// apimap reads ${MICROLINK_BASE_URL} via os.Getenv at LoadFile.
	// Push the cfg value into env so config remains the single source of truth.
	if err := os.Setenv("MICROLINK_BASE_URL", cfg.MicrolinkBaseURL); err != nil {
		return err
	}
	cfg.APIMap.Path = "clients.yaml"
	cfg.NATSMap.PublishersPath = "publishers.yaml"

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	svc, err := service.New[appctx.AppCtx, users.Claims](ctx, cfg.Config,
		service.WithOpenAPI(openapi.Info{
			Title:       "urlshort API",
			Version:     "0.1.0",
			Description: "gokit integration example — URL shortener.",
		}),
		service.WithAPIMapRegistration(func(e *apimap.Engine) {
			apimap.RegisterResponse[enrich.MicroLinkResp](e, "microlink.metadata")
		}),
		service.WithNATSMapRegistration(func(e *natsmap.Engine) {
			natsmap.RegisterPublisher[events.LinkCreated](e, "urlshort.link.created")
			natsmap.RegisterPublisher[events.LinkVisited](e, "urlshort.link.visited")
		}),
	)
	if err != nil {
		return err
	}
	defer svc.Close()

	if err := applyMigrations(ctx, svc.DB, "migrations/0001_init.sql"); err != nil {
		return err
	}
	if err := svc.NATS.EnsureStream(ctx, natsclient.StreamConfig{
		Name:     "URLSHORT",
		Subjects: []string{"urlshort.>"},
		MaxAge:   7 * 24 * time.Hour,
		Storage:  natsclient.StorageFile,
	}); err != nil {
		return err
	}

	fetcher := enrich.NewFetcher(svc.HTTPC, svc.APIMap, svc.Logger())
	usersSvc := users.NewService(svc.DB, svc.Hasher)
	linksSvc := links.NewService(svc.DB,
		fetcher.FetchMetadata,
		func(ctx context.Context, l links.Link) {
			events.PublishCreated(ctx, svc.NATSMap, svc.Logger(), events.LinkCreated{
				LinkID: l.ID, UserID: l.UserID, Code: l.Code,
				URL: l.OriginalURL, Title: l.Title, CreatedAt: l.CreatedAt,
			})
		},
		func(ctx context.Context, code, ua, ip string) {
			events.PublishVisited(ctx, svc.NATSMap, svc.Logger(), events.LinkVisited{
				Code: code, VisitedAt: time.Now(), UserAgent: ua, IP: ip,
			})
		},
	)

	svc.SetContextBuilder(appctx.NewContextBuilder(svc.Auth, svc.Logger()))
	users.RegisterHandlers(svc.Engine, usersSvc, svc.Auth)
	links.RegisterHandlers(svc.Engine, linksSvc, cfg.ShortURLBase)

	return svc.Run()
}

func applyMigrations(ctx context.Context, d *db.DB, path string) error {
	sqlBytes, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	_, err = d.Exec(ctx, string(sqlBytes))
	return err
}
