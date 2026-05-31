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
	"github.com/theizzatbek/gokit/clients/cache"
	natsclient "github.com/theizzatbek/gokit/clients/nats"
	"github.com/theizzatbek/gokit/clients/natsmap"
	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/db/outbox"
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

	// visitCounter is constructed AFTER service.New but referenced from
	// the natsmap registration callback (which runs INSIDE service.New
	// once buildDB has populated svc.DB but before subscriptions
	// open). Captured by pointer so the handler closure sees the
	// fully-built instance by the time the first event lands.
	var visitCounter *links.VisitCounter

	svc, err := service.New[appctx.AppCtx, users.Claims](ctx, cfg.Config,
		service.WithValidator(v),
		service.WithAPIMap(),
		service.WithNATSMap(),
		service.WithRoutes(),
		service.WithOpenAPI(),
		// 64 KiB is plenty for the largest payload urlshort actually
		// accepts (a long URL + title + description). Reject anything
		// bigger at the edge — Fiber returns 413 before the handler
		// allocates a body buffer.
		service.WithBodyLimit(64*1024),
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
			natsmap.RegisterBatchedHandler[events.LinkVisited](e, "link_visit_counter",
				func(ctx context.Context, batch []natsclient.Msg[events.LinkVisited]) error {
					return visitCounter.Handle(ctx, batch)
				})
		}),
	)
	if err != nil {
		return err
	}
	defer svc.Close()

	for _, mig := range []string{
		"migrations/0001_init.sql",
		"migrations/0002_idempotent_links.sql",
	} {
		if err := applyMigrations(ctx, svc.DB, mig); err != nil {
			return err
		}
	}
	if _, err := svc.DB.Exec(ctx, outbox.Schema()); err != nil {
		return fmt.Errorf("urlshort: apply outbox schema: %w", err)
	}

	// Outbox worker: drains the `outbox` table the Create transaction
	// writes to. Publishes raw JSON bytes onto the LinkCreated subject
	// via natsmap.PublishRaw — bypasses the typed codec because the
	// bytes are already JSON-encoded and re-encoding would re-order keys.
	w, err := outbox.NewWorker(svc.DB, func(ctx context.Context, e outbox.Event) error {
		return natsmap.PublishRaw(ctx, svc.NATSMap, e.EventType, e.Payload, e.Headers)
	}, outbox.WithLogger(svc.Logger()))
	if err != nil {
		return err
	}
	if err := w.Start(ctx); err != nil {
		return err
	}
	svc.OnShutdown(w.Stop)

	linkCache := cache.For[links.CachedLink](svc.Redis, "urlshort:link:")

	fetcher := enrich.NewFetcher(svc.APIMap, svc.Logger())
	usersSvc := users.NewService(svc.DB, svc.Hasher)
	pub := events.NewPublisher(svc.NATSMap, svc.Logger())
	linksSvc := links.NewService(svc.DB, fetcher.FetchMetadata, pub, linkCache)
	visitCounter = links.NewVisitCounter(svc.DB, svc.Logger())
	// natsmap.Runtime.Drain (invoked by service.Close) owns the
	// subscription lifecycle — no explicit Close on the counter
	// since the batched fetch loop lives inside natsmap.

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
