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
	"embed"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-playground/validator/v10"

	"github.com/theizzatbek/gokit/clients/apimap"
	"github.com/theizzatbek/gokit/clients/cache"
	natsclient "github.com/theizzatbek/gokit/clients/nats"
	"github.com/theizzatbek/gokit/clients/natsmap"
	"github.com/theizzatbek/gokit/db/outbox"
	"github.com/theizzatbek/gokit/examples/urlshort/internal/appctx"
	"github.com/theizzatbek/gokit/examples/urlshort/internal/config"
	"github.com/theizzatbek/gokit/examples/urlshort/internal/enrich"
	"github.com/theizzatbek/gokit/examples/urlshort/internal/events"
	"github.com/theizzatbek/gokit/examples/urlshort/internal/links"
	"github.com/theizzatbek/gokit/examples/urlshort/internal/users"
	"github.com/theizzatbek/gokit/service"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// urlshortMigrations strips the "migrations/" prefix so the kit's
// migrate.Parse walks file names directly.
func urlshortMigrations() fs.FS {
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		panic(err)
	}
	return sub
}

// linksStatsJob returns a JobFn that logs aggregate link / visit
// stats. Demonstrates the shape of a cron job wired through
// [service.Service.AddCron] — Sentry Crons check-ins fire
// automatically when [service.WithSentry] is set; the job needs to
// know nothing about the monitor.
func linksStatsJob(svc *service.Service[appctx.AppCtx, users.Claims]) service.JobFn {
	return func(ctx context.Context) error {
		var (
			linkCount  int
			totalViews int64
		)
		row := svc.DB.QueryRow(ctx,
			`SELECT count(*), COALESCE(SUM(visit_count), 0) FROM links`)
		if err := row.Scan(&linkCount, &totalViews); err != nil {
			return err
		}
		svc.Logger().Info("daily-stats",
			"links", linkCount, "total_views", totalViews)
		return nil
	}
}

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
		// Embedded migrations applied via the kit's db/migrate runner.
		// schema_migrations bookkeeping is automatic. No more
		// for-range ReadFile/Exec loop in main.go.
		service.WithMigrations(urlshortMigrations()),
		// Transactional outbox: writes LinkCreated inside the same
		// db.Tx as the link insert, drains via natsmap.PublishRaw.
		// LISTEN/NOTIFY auto-wakes the worker within ~ms of commit.
		// 7-day retention keeps published rows for replay tooling.
		service.WithOutbox(
			outbox.WithRetention(7*24*time.Hour),
		),
		service.WithOutboxAutoSchema(),
	)
	if err != nil {
		return err
	}
	defer svc.Close()

	// Daily housekeeping cron: log link + visit totals so dashboards
	// can graph creation cadence. Registered post-build because the
	// job closes over svc.DB / svc.Logger — built inside service.New.
	// Real workloads would aggregate per-user or feed a downstream
	// analytics pipeline; this is the shape of cron wiring.
	if err := svc.AddCron("daily-stats", "0 3 * * *", linksStatsJob(svc)); err != nil {
		return err
	}

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
