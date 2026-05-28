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

	"github.com/go-playground/validator/v10"

	"github.com/theizzatbek/gokit/clients/apimap"
	"github.com/theizzatbek/gokit/clients/natsmap"
	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/examples/urlshort/internal/appctx"
	"github.com/theizzatbek/gokit/examples/urlshort/internal/config"
	"github.com/theizzatbek/gokit/examples/urlshort/internal/enrich"
	"github.com/theizzatbek/gokit/examples/urlshort/internal/events"
	"github.com/theizzatbek/gokit/examples/urlshort/internal/links"
	"github.com/theizzatbek/gokit/examples/urlshort/internal/users"
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

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Customise the engine's validator with the links-package safe_url
	// rule so CreateRequest.URL rejects loopback / private addresses
	// (SSRF guard). Other built-in tags (required, url, email, min, …)
	// keep their default semantics.
	v := validator.New(validator.WithRequiredStructEnabled())
	if err := links.RegisterValidators(v); err != nil {
		return err
	}

	svc, err := service.New[appctx.AppCtx, users.Claims](ctx, cfg.Config,
		service.WithValidator(v),
		service.WithAPIMap(),
		service.WithNATSMap(),
		service.WithRoutes(),
		service.WithOpenAPI(),
		service.WithAPIMapEnv(map[string]string{
			"MICROLINK_BASE_URL": cfg.MicrolinkBaseURL,
		}),
		service.WithAPIMapRegistration(func(e *apimap.Engine) {
			apimap.RegisterResponse[enrich.MicroLinkResp](e, "microlink.metadata")
			apimap.RegisterResponse[[]byte](e, "web.fetch")
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

	fetcher := enrich.NewFetcher(svc.APIMap, svc.Logger())
	usersSvc := users.NewService(svc.DB, svc.Hasher)
	pub := events.NewPublisher(svc.NATSMap, svc.Logger())
	linksSvc := links.NewService(svc.DB, fetcher.FetchMetadata, pub)

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
